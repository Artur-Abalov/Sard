// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import dev.sard.server.metrics.SardMetricNames
import io.micrometer.core.instrument.Gauge
import io.micrometer.core.instrument.MeterRegistry
import org.slf4j.LoggerFactory
import java.sql.Connection

/** The oldest PostgreSQL the server starts on, and the newest it was tested on (DB1). */
const val MIN_POSTGRES_MAJOR = 16
const val TESTED_POSTGRES_MAJOR = 18

private const val DOCS = "docs/operator/16-postgresql.md"
private val C_UTF8 = setOf("C.UTF-8", "C.utf8")
private val UTC = setOf("UTC", "Etc/UTC")

/**
 * What the start reads from the server's database (DB1, D25): the parameters initdb fixed when the database was
 * created, which no later setting changes, and the time zone of the server process. [localeProvider] is
 * pg_database.datlocprovider: `b` builtin (PostgreSQL 17+), `c` libc, `i` ICU; [locale] is the builtin or ICU
 * locale, null under libc.
 */
data class DatabaseSettings(
    val versionNum: Int,
    val version: String,
    val dataChecksums: Boolean,
    val encoding: String,
    val localeProvider: String,
    val collate: String,
    val ctype: String,
    val locale: String?,
    val logTimezone: String,
) {
    val major: Int get() = versionNum / 10_000
}

/** How a database differs from one a new installation creates; [label] is the `reason` of the metric. */
enum class DatabaseMismatch(
    val label: String,
) {
    DATA_CHECKSUMS("data_checksums"),
    ENCODING("encoding"),
    LOCALE("locale"),
    TIMEZONE("timezone"),
    VERSION("version"),
}

/**
 * The server does not start: PostgreSQL older than [MIN_POSTGRES_MAJOR]. The message starts with
 * "Database startup refused: DATABASE_VERSION_UNSUPPORTED:" and names the version found.
 */
class DatabaseVersionUnsupported(
    version: String,
) : IllegalStateException(
        "Database startup refused: DATABASE_VERSION_UNSUPPORTED: PostgreSQL $version, Sard needs PostgreSQL " +
            "$MIN_POSTGRES_MAJOR or newer ($DOCS)",
    )

fun refuseUnsupportedVersion(settings: DatabaseSettings) {
    if (settings.major < MIN_POSTGRES_MAJOR) throw DatabaseVersionUnsupported(settings.version)
}

private fun DatabaseSettings.localeIsCUtf8() =
    when (localeProvider) {
        "b" -> locale in C_UTF8
        "c" -> collate in C_UTF8 && ctype in C_UTF8
        else -> false
    }

/** What differs from a new installation's database; nothing is wrong with it, it is only not what Sard creates. */
fun databaseMismatches(settings: DatabaseSettings): Set<DatabaseMismatch> =
    buildSet {
        if (!settings.dataChecksums) add(DatabaseMismatch.DATA_CHECKSUMS)
        if (settings.encoding != "UTF8") add(DatabaseMismatch.ENCODING)
        if (!settings.localeIsCUtf8()) add(DatabaseMismatch.LOCALE)
        if (settings.logTimezone !in UTC) add(DatabaseMismatch.TIMEZONE)
        if (settings.major > TESTED_POSTGRES_MAJOR) add(DatabaseMismatch.VERSION)
    }

private fun DatabaseSettings.localeText() =
    when (localeProvider) {
        "b" -> "builtin $locale"
        "i" -> "icu $locale"
        else -> "libc $collate/$ctype"
    }

private fun DatabaseSettings.describe(mismatch: DatabaseMismatch) =
    when (mismatch) {
        DatabaseMismatch.DATA_CHECKSUMS -> "data_checksums=off (want on)"
        DatabaseMismatch.ENCODING -> "encoding=$encoding (want UTF8)"
        DatabaseMismatch.LOCALE -> "locale=${localeText()} (want C.UTF-8)"
        DatabaseMismatch.TIMEZONE -> "timezone=$logTimezone (want UTC)"
        DatabaseMismatch.VERSION -> "version=$version (tested up to $TESTED_POSTGRES_MAJOR)"
    }

/** The line of the start log: every difference with its value, and where to read what to do. */
fun databaseWarning(
    settings: DatabaseSettings,
    mismatches: Set<DatabaseMismatch>,
): String =
    "the database differs from what a new installation creates: " +
        mismatches.joinToString(", ") { settings.describe(it) } + "; the server works on it, see $DOCS"

/**
 * Only reads: settings any role may see, the row of the current database in pg_database (to_jsonb, because its
 * columns differ between versions: datlocale is PostgreSQL 17+, daticulocale before). The time zone is
 * log_timezone: the JDBC driver sets the session's TimeZone from the JVM, so `SHOW TimeZone` would report the
 * server process' zone, not the database's.
 */
fun readDatabaseSettings(connection: Connection): DatabaseSettings =
    connection.createStatement().use { statement ->
        statement
            .executeQuery(
                """
                select current_setting('server_version_num')::int as version_num,
                       current_setting('server_version') as version,
                       current_setting('data_checksums') = 'on' as data_checksums,
                       pg_encoding_to_char(d.encoding) as encoding,
                       to_jsonb(d) ->> 'datlocprovider' as locale_provider,
                       d.datcollate as collate, d.datctype as ctype,
                       coalesce(to_jsonb(d) ->> 'datlocale', to_jsonb(d) ->> 'daticulocale') as locale,
                       current_setting('log_timezone') as log_timezone
                from pg_database d where d.datname = current_database()
                """.trimIndent(),
            ).use { row ->
                check(row.next()) { "the current database is not in pg_database" }
                DatabaseSettings(
                    versionNum = row.getInt("version_num"),
                    version = row.getString("version").substringBefore(" "),
                    dataChecksums = row.getBoolean("data_checksums"),
                    encoding = row.getString("encoding"),
                    localeProvider = row.getString("locale_provider") ?: "c",
                    collate = row.getString("collate"),
                    ctype = row.getString("ctype"),
                    locale = row.getString("locale"),
                    logTimezone = row.getString("log_timezone"),
                )
            }
    }

/**
 * The check of the start (DB1), before any migration: an unsupported version refuses the start; any other
 * difference is one WARN line and `sard.database.settings.mismatch{reason}` at 1, because an external database may
 * be set up otherwise on purpose. Every reason is registered, at 0 when it does not apply.
 */
class DatabaseSettingsCheck(
    private val meters: MeterRegistry,
) {
    private val log = LoggerFactory.getLogger(DatabaseSettingsCheck::class.java)

    fun check(connection: Connection) {
        val settings = readDatabaseSettings(connection)
        refuseUnsupportedVersion(settings)
        val found = databaseMismatches(settings)
        for (mismatch in DatabaseMismatch.entries) {
            val value = if (mismatch in found) 1.0 else 0.0
            Gauge
                .builder(SardMetricNames.DATABASE_SETTINGS_MISMATCH) { value }
                .tag("reason", mismatch.label)
                .register(meters)
        }
        if (found.isNotEmpty()) log.warn(databaseWarning(settings, found))
    }
}
