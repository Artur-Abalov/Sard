// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import dev.sard.server.scheduler.CronSchedule
import dev.sard.server.scheduler.InvalidSchedule
import dev.sard.server.scheduler.ScheduleDraft
import dev.sard.server.scheduler.ScheduleField
import org.springframework.boot.context.properties.ConfigurationProperties
import java.time.ZoneId

private const val JDBC_PREFIX = "jdbc:postgresql:"
private const val DEFAULT_PORT = 5432
private const val DEFAULT_DATABASE = "postgres"

/** Where the agent next to the server reaches the database Sard runs on. */
data class DatabaseTarget(
    val host: String,
    val port: Int,
    val name: String,
)

/** What the two system sources are made of (F6); every path is one inside the self-agent's container. */
data class SelfBackupSettings(
    val database: DatabaseTarget,
    val databaseUser: String,
    val passwordSecret: String,
    val databaseTlsMode: String,
    val pgDumpPath: String,
    val pkiDir: String,
    val installDir: String,
    val installExclude: List<String>,
    val schedule: ScheduleDraft,
)

/**
 * `sard.self-backup.*` (F6, ADR 00XX-draft-self-backup). The defaults fit deploy/docker-compose.yml: the
 * database is the server's own (its datasource URL), read by the role sard_self with the password the
 * server writes into the channel (secret sard-db of self-agent); the CA directory and the installation
 * directory are mounted read-only into self-agent at [pkiDir] and [installDir]. An empty [timezone] means
 * the server's own zone; [databaseHost] empty means: take host, port and name from the datasource URL.
 */
@ConfigurationProperties("sard.self-backup")
data class SelfBackupProperties(
    val cron: String = "0 3 * * *",
    val timezone: String = "",
    val databaseHost: String = "",
    val databasePort: Int = DEFAULT_PORT,
    val databaseName: String = "",
    val databaseUser: String = "sard_self",
    val passwordSecret: String = "sard-db",
    /** The compose network is private to the installation; an external PostgreSQL wants require or verify-full. */
    val databaseTlsMode: String = "disable",
    val pgDumpPath: String = "/usr/lib/postgresql/18/bin/pg_dump",
    val pkiDir: String = "/var/lib/sard/pki",
    val installDir: String = "/etc/sard/install",
    /** Manual backups and offline archives lie next to the compose file; they are not the configuration. */
    val installExclude: List<String> = listOf("*.dump", "*.tgz", "*.tar", "*.tar.gz", "*.zst"),
) {
    /** Refuses, naming the variable, a schedule that does not parse or a database it cannot find. */
    fun settings(
        datasourceUrl: String,
        serverZone: ZoneId,
    ): SelfBackupSettings =
        SelfBackupSettings(
            database = database(datasourceUrl),
            databaseUser = databaseUser,
            passwordSecret = passwordSecret,
            databaseTlsMode = databaseTlsMode,
            pgDumpPath = pgDumpPath,
            pkiDir = pkiDir,
            installDir = installDir,
            installExclude = installExclude,
            schedule = schedule(serverZone),
        )

    private fun schedule(serverZone: ZoneId): ScheduleDraft {
        val zone = timezone.ifEmpty { serverZone.id }
        try {
            CronSchedule.parse(cron, zone)
        } catch (e: InvalidSchedule) {
            val variable =
                when (e.field) {
                    ScheduleField.CRON -> "SARD_SELF_BACKUP_CRON"
                    ScheduleField.TIMEZONE -> "SARD_SELF_BACKUP_TIMEZONE"
                }
            throw IllegalArgumentException("$variable: ${e.message}", e)
        }
        return ScheduleDraft(cron, zone, true)
    }

    private fun database(datasourceUrl: String): DatabaseTarget {
        if (databaseHost.isNotEmpty()) {
            return DatabaseTarget(databaseHost, databasePort, databaseName.ifEmpty { "sard" })
        }
        val parsed = runCatching { databaseOf(datasourceUrl) }.getOrNull()
        requireNotNull(parsed) {
            "the self-backup cannot find the database in the datasource URL; " +
                "set SARD_SELF_BACKUP_DATABASE_HOST, SARD_SELF_BACKUP_DATABASE_PORT and SARD_SELF_BACKUP_DATABASE_NAME"
        }
        return if (databaseName.isEmpty()) parsed else parsed.copy(name = databaseName)
    }
}

/**
 * The first host of a PostgreSQL JDBC URL: `jdbc:postgresql://host[:port][,…]/database[?…]` or
 * `jdbc:postgresql:database`. Throws [IllegalArgumentException] for anything else.
 */
internal fun databaseOf(url: String): DatabaseTarget {
    require(url.startsWith(JDBC_PREFIX)) { "not a PostgreSQL JDBC URL" }
    val rest = url.removePrefix(JDBC_PREFIX).substringBefore('?')
    if (!rest.startsWith("//")) return DatabaseTarget("localhost", DEFAULT_PORT, rest)
    val authority = rest.removePrefix("//").substringBefore('/')
    val name = rest.removePrefix("//").substringAfter('/', "").ifEmpty { DEFAULT_DATABASE }
    val first = authority.substringBefore(',')
    val (host, port) = hostAndPort(first)
    require(host.isNotEmpty()) { "no host" }
    return DatabaseTarget(host, port, name)
}

private fun hostAndPort(address: String): Pair<String, Int> {
    if (address.startsWith('[')) {
        val host = address.substring(1, address.indexOf(']'))
        val port = address.substringAfter("]:", "").ifEmpty { null }
        return host to (port?.toInt() ?: DEFAULT_PORT)
    }
    val port = address.substringAfter(':', "").ifEmpty { null }
    return address.substringBefore(':') to (port?.toInt() ?: DEFAULT_PORT)
}
