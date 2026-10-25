// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import dev.sard.server.EmbeddedPostgres
import dev.sard.server.TestImages
import dev.sard.server.onboarding.Database
import dev.sard.server.onboarding.Installation
import dev.sard.server.onboarding.RunningServer
import io.micrometer.prometheusmetrics.PrometheusMeterRegistry
import org.junit.jupiter.api.io.TempDir
import org.testcontainers.postgresql.PostgreSQLContainer
import org.testcontainers.utility.DockerImageName
import java.nio.file.Path
import java.sql.DriverManager
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val WARNING = "the database differs from what a new installation creates"
private const val REFUSED = "Database startup refused: DATABASE_VERSION_UNSUPPORTED:"
private const val PUBLIC_TABLES = "select count(*) from pg_tables where schemaname = 'public'"

/** The pinned image with the image's own initdb, as every installation before DB1. */
private val imageDefaults: PostgreSQLContainer by lazy {
    val image = DockerImageName.parse(EmbeddedPostgres.image).asCompatibleSubstituteFor("postgres")
    PostgreSQLContainer(image).also { it.start() }
}

private val newInstallation: PostgreSQLContainer by lazy { EmbeddedPostgres.container().also { it.start() } }

/** External databases: the oldest supported major, and one below it. */
private fun external(major: Int): PostgreSQLContainer {
    val image = DockerImageName.parse(TestImages.pinned("postgres:$major")).asCompatibleSubstituteFor("postgres")
    return PostgreSQLContainer(image).also { it.start() }
}

private fun PostgreSQLContainer.settings(
    user: String = username,
    password: String = this.password,
) = DriverManager.getConnection(jdbcUrl, user, password).use(::readDatabaseSettings)

private fun PostgreSQLContainer.database() = Database(jdbcUrl, username, password)

/**
 * Rules "Сервер видит базу, созданную не так, как новая установка" (DB1): what is read from real PostgreSQL servers,
 * and what a real start says about it.
 */
class DatabaseSettingsIntegrationTest {
    @TempDir
    lateinit var tmp: Path

    private val running = mutableListOf<RunningServer>()

    @AfterTest
    fun `stop every server`() {
        running.forEach { runCatching { it.close() } }
    }

    private fun start(database: Database) = Installation(tmp, database).start().also { running += it }

    private fun RunningServer.mismatchSeries() =
        bean(PrometheusMeterRegistry::class.java)
            .scrape()
            .lines()
            .filter { it.startsWith("sard_database_settings_mismatch{") }
            .associate { it.substringAfter("reason=\"").substringBefore("\"") to it.substringAfterLast(" ").toDouble() }

    @Test
    fun `База новой установки — PostgreSQL 18, контрольные суммы, UTF8, встроенная C UTF-8, UTC`() {
        val settings = newInstallation.settings()

        assertEquals(18, settings.major)
        assertEquals(
            listOf(true, "UTF8", "b", "C.UTF-8", "UTC"),
            with(settings) {
                listOf(dataChecksums, encoding, localeProvider, locale, logTimezone)
            },
        )
        assertEquals(emptySet(), databaseMismatches(settings))
    }

    @Test
    fun `Роль без прав суперпользователя читает то же самое`() {
        newInstallation.createConnection("").use {
            it.createStatement().execute("create role db1_reader login password 'db1-reader'")
        }

        assertEquals(newInstallation.settings(), newInstallation.settings("db1_reader", "db1-reader"))
    }

    @Test
    fun `База с initdb образа по умолчанию отличается одной локалью`() {
        val settings = imageDefaults.settings()

        assertEquals("c", settings.localeProvider)
        assertEquals(setOf(DatabaseMismatch.LOCALE), databaseMismatches(settings))
    }

    @Test
    fun `PostgreSQL 16 читается и не отклоняется`() {
        val settings = external(16).use { it.settings() }

        assertEquals(16, settings.major)
        refuseUnsupportedVersion(settings)
        assertTrue(DatabaseMismatch.VERSION !in databaseMismatches(settings))
    }

    @Test
    fun `Сервер на базе с initdb образа стартует с предупреждением и метрикой локали`() {
        val server = start(imageDefaults.freshDatabase())

        val warnings = server.startLog.filter { WARNING in it.text }
        assertEquals(1, warnings.size, server.logText)
        assertTrue("locale=libc en_US.utf8/en_US.utf8 (want C.UTF-8)" in warnings.single().text, warnings.toString())
        val expected =
            DatabaseMismatch.entries.associate { it.label to if (it == DatabaseMismatch.LOCALE) 1.0 else 0.0 }
        assertEquals(expected, server.mismatchSeries())
    }

    @Test
    fun `Сервер на базе новой установки стартует без предупреждения, все метрики 0`() {
        val server = start(newInstallation.freshDatabase())

        assertFalse(server.startLog.any { WARNING in it.text }, server.logText)
        assertEquals(DatabaseMismatch.entries.associate { it.label to 0.0 }, server.mismatchSeries())
    }

    @Test
    fun `Сервер на PostgreSQL 15 не стартует и ничего не создаёт`() {
        external(15).use { pg15 ->
            val (failure, _) = Installation(tmp, pg15.database()).startFailing()

            val messages = generateSequence(failure) { it.cause }.mapNotNull { it.message }.toList()
            assertTrue(
                messages.any { it.startsWith("$REFUSED PostgreSQL 15.") },
                messages.toString(),
            )
            assertFailsWith<DatabaseVersionUnsupported> { refuseUnsupportedVersion(pg15.settings()) }
            pg15.createConnection("").use {
                val tables = it.createStatement().executeQuery(PUBLIC_TABLES)
                tables.next()
                assertEquals(0, tables.getInt(1), "the refused start created tables")
            }
        }
    }
}

private val counter =
    java.util.concurrent.atomic
        .AtomicInteger()

/** A database of its own in the cluster: every start migrates an empty one. */
private fun PostgreSQLContainer.freshDatabase(): Database {
    val name = "db1_${counter.incrementAndGet()}"
    createConnection("").use { it.createStatement().execute("create database $name") }
    return Database("jdbc:postgresql://$host:$firstMappedPort/$name", username, password)
}
