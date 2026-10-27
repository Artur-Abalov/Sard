// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.recovery

import dev.sard.server.TestAdmin
import dev.sard.server.database.MigrationSettings
import dev.sard.server.database.Preparation
import dev.sard.server.database.coreConfiguration
import dev.sard.server.onboarding.Database
import dev.sard.server.onboarding.Installation
import dev.sard.server.onboarding.RunningServer
import dev.sard.server.onboarding.StartOptions
import dev.sard.server.onboarding.TestPostgres
import org.junit.jupiter.api.io.TempDir
import org.springframework.boot.info.BuildProperties
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.jdbc.datasource.DriverManagerDataSource
import java.nio.file.Files
import java.nio.file.Path
import java.util.Properties
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val FUTURE = "299901010000"
private const val CODE_LINE = "SARD SETUP CODE"
private const val HISTORY =
    "select installed_rank, version, description, checksum, success from flyway_schema_history order by 1"

/** The migrations of this build: the version in the file name, and the description. */
private val MIGRATIONS: List<Pair<String, String>> =
    checkNotNull(Thread.currentThread().contextClassLoader.getResource("db/migration")).let { dir ->
        Files.list(Path.of(dir.toURI())).use { files ->
            files
                .map { it.fileName.toString() }
                .map { Regex("V(\\d+)__(.+)\\.sql").matchEntire(it)!!.destructured }
                .map { (version, description) -> version to description }
                .toList()
        }
    }

/** M: the newest migration of this build. */
private val LAST_MIGRATION = MIGRATIONS.maxBy { it.first.toLong() }.first

private fun build(version: String) = BuildProperties(Properties().apply { setProperty("version", version) })

private fun Throwable.messages(): List<String> = generateSequence(this) { it.cause }.mapNotNull { it.message }.toList()

/**
 * Rule "Сервер не стартует на базе от более новой версии Sard" of docs/specs/server/server-recovery.feature:
 * real servers started one after another over one installation.
 */
class DatabaseNewerThanServerIntegrationTest {
    @TempDir
    lateinit var tmp: Path

    private val installation by lazy { Installation(tmp) }
    private val running = mutableListOf<RunningServer>()

    @AfterTest
    fun `stop every server`() {
        running.forEach { runCatching { it.close() } }
    }

    private fun jdbc(): JdbcTemplate {
        val database = installation.database
        return JdbcTemplate(DriverManagerDataSource(database.url, database.user, database.password))
    }

    /** A database with all migrations applied and one more row, of [version], as a newer server leaves it. */
    private fun databaseWithMigration(
        version: String,
        description: String = "from_the_future",
        success: Boolean = true,
    ) {
        installation.start().close()
        // sard_app may not write the history of migrations: the superuser of the cluster plays the newer server.
        TestAdmin.of(jdbc()).update(
            "insert into flyway_schema_history (installed_rank, version, description, type, script, checksum, " +
                "installed_by, installed_on, execution_time, success) " +
                "select max(installed_rank) + 1, ?, ?, 'SQL', 'V${version}__$description.sql', 1, 'sard', now(), " +
                "1, ? " +
                "from flyway_schema_history",
            version,
            description,
            success,
        )
    }

    private fun history() = jdbc().queryForList(HISTORY).map { it.toString() }

    private fun names(directory: Path): List<String> =
        if (Files.notExists(directory)) {
            emptyList()
        } else {
            Files.list(directory).use { found -> found.map { it.fileName.toString() }.sorted().toList() }
        }

    @Test
    fun `Сервер на базе с миграцией новее своих отказывается стартовать`() {
        databaseWithMigration(FUTURE)

        val (failure, _) = installation.startFailing()

        val message = generateSequence(failure) { it.cause }.mapNotNull { it.message }.toList()
        assertTrue(
            message.any { it.startsWith("Database startup refused: DATABASE_NEWER_THAN_SERVER:") },
            message.toString(),
        )
    }

    @Test
    fun `Отказ из-за новой базы называет обе версии миграций, версию сервера и страницу восстановления`() {
        databaseWithMigration(FUTURE)

        val (failure, _) = installation.startFailing(StartOptions(beans = mapOf("buildProperties" to build("0.1.0"))))

        val message = failure.messages().first { "DATABASE_NEWER_THAN_SERVER" in it }
        val page = "docs/operator/12-server-recovery.md"
        val expectations = listOf(FUTURE, "from_the_future", LAST_MIGRATION, "0.1.0", page)
        for (expected in expectations) {
            assertTrue(expected in message, "$expected not in $message")
        }
    }

    @Test
    fun `Отказ из-за новой базы ничего не меняет`() {
        databaseWithMigration(FUTURE)
        val before = history()
        val ca = names(installation.pkiDir)

        val (_, log) = installation.startFailing()

        assertEquals(before, history())
        assertEquals(ca, names(installation.pkiDir))
        assertEquals(emptyList(), names(installation.selfDir))
        assertFalse(log.any { CODE_LINE in it.text }, "a code was printed")
    }

    @Test
    fun `Отказ из-за новой базы повторяется при каждом старте`() {
        databaseWithMigration(FUTURE)
        installation.startFailing()

        val (failure, _) = installation.startFailing()

        assertTrue(failure.messages().any { "DATABASE_NEWER_THAN_SERVER" in it }, failure.messages().toString())
    }

    @Test
    fun `Сервер на базе своей версии стартует без новых миграций`() {
        installation.start().close()
        val before = history()

        installation.start().close()

        assertEquals(before, history())
    }

    /** The migrations of this build up to and including [target], as migrate would stop there. */
    private fun coreAt(
        database: Database,
        target: String,
    ) {
        val settings = MigrationSettings(database.url, "sard_migrator", Preparation.password("sard_migrator"))
        coreConfiguration(settings).target(target).load().migrate()
    }

    @Test
    fun `Сервер на базе более старой версии стартует, когда migrate применил недостающие миграции`() {
        val older = TestPostgres.newDatabase(migrated = false)
        val sandbox = Installation(tmp, older)
        coreAt(older, "202610111200")

        val (refused, _) = sandbox.startFailing()
        assertTrue("MIGRATIONS_NOT_APPLIED" in refused.messages().joinToString("\n"), "the server does not migrate")
        Preparation.migrate(older.url)
        sandbox.start().close()

        val applied =
            JdbcTemplate(
                DriverManagerDataSource(older.url, older.user, older.password),
            ).queryForList("select version from flyway_schema_history where success", String::class.java)
        assertTrue(LAST_MIGRATION in applied, applied.toString())
    }

    @Test
    fun `Неизвестная миграция старше последней известной не считается новой базой`() {
        databaseWithMigration("202601010000", "unknown_older")

        val (failure, _) = installation.startFailing()

        val text = failure.messages().joinToString("\n")
        assertTrue("202601010000" in text, text)
        assertFalse("DATABASE_NEWER_THAN_SERVER" in text, text)
    }

    @Test
    fun `Неудачная строка будущей миграции тоже останавливает старт без изменений`() {
        databaseWithMigration(FUTURE, success = false)
        val before = history()

        installation.startFailing()

        assertEquals(before, history())
    }
}
