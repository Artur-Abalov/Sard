// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.recovery

import dev.sard.server.onboarding.Installation
import dev.sard.server.onboarding.RunningServer
import dev.sard.server.onboarding.StartOptions
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
private const val HISTORY = "select installed_rank, version, description, checksum, success from flyway_schema_history order by 1"

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

    private fun jdbc() = installation.database.let { JdbcTemplate(DriverManagerDataSource(it.url, it.user, it.password)) }

    /** A database where the server applied all its migrations, and one more row of [version], as a newer server leaves. */
    private fun databaseWithMigration(
        version: String,
        description: String = "from_the_future",
        success: Boolean = true,
    ) {
        installation.start().close()
        jdbc().update(
            "insert into flyway_schema_history (installed_rank, version, description, type, script, checksum, " +
                "installed_by, installed_on, execution_time, success) " +
                "select max(installed_rank) + 1, ?, ?, 'SQL', 'V${version}__$description.sql', 1, 'sard', now(), 1, ? " +
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
        for (expected in listOf(FUTURE, "from_the_future", LAST_MIGRATION, "0.1.0", "docs/operator/12-server-recovery.md")) {
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

    @Test
    fun `Сервер на базе более старой версии применяет недостающие миграции и стартует`() {
        installation.database.let {
            org.flywaydb.core.Flyway
                .configure()
                .dataSource(it.url, it.user, it.password)
                .target("202610111200")
                .load()
                .migrate()
        }

        installation.start().close()

        val applied = jdbc().queryForList("select version from flyway_schema_history where success", String::class.java)
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
