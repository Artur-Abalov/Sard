// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import dev.sard.server.onboarding.Database
import dev.sard.server.onboarding.TestPostgres
import org.flywaydb.core.Flyway
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.jdbc.datasource.DriverManagerDataSource
import java.nio.file.Files
import java.nio.file.Path
import java.sql.DriverManager
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull

private const val PREFIX = "Database startup refused: EXTENSION_MIGRATIONS_NOT_APPLIED:"

/** Rule "Сервер не стартует, пока не применены миграции расширений с classpath": the check on its own. */
class ExtensionMigrationsGuardTest {
    private fun extensionDir(vararg scripts: Pair<String, String>): Path {
        val dir = Files.createTempDirectory("ext-migrations")
        scripts.forEach { (name, sql) -> Files.writeString(dir.resolve(name), sql) }
        return dir
    }

    private fun flyway(
        database: Database,
        dir: Path,
        user: String = database.user,
        password: String = database.password,
    ): Flyway =
        Flyway
            .configure()
            .dataSource(database.url, user, password)
            .schemas("sard")
            .defaultSchema("sard")
            .locations("filesystem:$dir")
            .table("flyway_schema_history_extensions")
            .baselineOnMigrate(true)
            .baselineVersion("0")
            .load()

    /** As the migrate step does: the history is readable by the server's role, which default privileges give it. */
    private fun migrateAsAdmin(
        database: Database,
        dir: Path,
    ) {
        val admin = checkNotNull(database.admin)
        flyway(database, dir, admin.user, admin.password).migrate()
        JdbcTemplate(DriverManagerDataSource(database.url, admin.user, admin.password))
            .execute("grant select on sard.flyway_schema_history_extensions to sard_app")
    }

    private val sample = "V1__sample_events.sql" to "create table sample_events (id int);"

    private fun refusal(flyway: Flyway) = assertFailsWith<ExtensionMigrationsNotApplied> { check(flyway) }

    private fun check(flyway: Flyway) = requireExtensionsMigrated(flyway)

    @Test
    fun `Неприменённая миграция расширения останавливает старт`() {
        val database = TestPostgres.newDatabase()

        val message = assertNotNull(refusal(flyway(database, extensionDir(sample))).message)

        assertEquals(true, message.startsWith(PREFIX), message)
    }

    @Test
    fun `Отказ называет историю расширений, первую миграцию и службу migrate`() {
        val database = TestPostgres.newDatabase()

        val message = assertNotNull(refusal(flyway(database, extensionDir(sample))).message)

        assertContains(message, "sard.flyway_schema_history_extensions")
        assertContains(message, "1 migration(s)")
        assertContains(message, "sample_events")
        assertContains(message, "migrate")
        assertContains(message, "docs/operator/16-postgresql.md")
    }

    @Test
    fun `Неудачно записанная миграция расширения останавливает старт`() {
        val database = TestPostgres.newDatabase()
        val dir = extensionDir(sample)
        migrateAsAdmin(database, dir)
        val admin = checkNotNull(database.admin)
        JdbcTemplate(DriverManagerDataSource(database.url, admin.user, admin.password))
            .update("update sard.flyway_schema_history_extensions set success = false where version = '1'")

        val message = assertNotNull(refusal(flyway(database, dir)).message)

        assertEquals(true, message.startsWith(PREFIX), message)
    }

    @Test
    fun `Применённая миграция расширения проходит проверку`() {
        val database = TestPostgres.newDatabase()
        val dir = extensionDir(sample)
        migrateAsAdmin(database, dir)

        requireExtensionsMigrated(flyway(database, dir))
    }

    @Test
    fun `Расширение без миграций стартует без таблицы истории расширений`() {
        val database = TestPostgres.newDatabase()

        requireExtensionsMigrated(flyway(database, extensionDir()))

        val query = "select table_name from information_schema.tables where table_name like 'flyway_schema_history_%'"
        val tables =
            JdbcTemplate(DriverManagerDataSource(database.url, database.user, database.password)).queryForList(query)
        assertEquals(emptyList(), tables)
    }

    @Test
    fun `Расположение миграций расширений берётся из настройки migrate, а не из умолчания`() {
        val database = TestPostgres.newDatabase()
        val custom = extensionDir(sample)
        val core = flyway(database, extensionDir())

        val reader =
            DriverManager.getConnection(database.url, database.user, database.password).use { connection ->
                val refusal =
                    assertFailsWith<ExtensionMigrationsNotApplied> {
                        requireExtensionsMigrated(extensionReader(core, connection, listOf("filesystem:$custom")))
                    }
                refusal.message
            }

        assertEquals(true, assertNotNull(reader).startsWith(PREFIX), reader)
    }
}
