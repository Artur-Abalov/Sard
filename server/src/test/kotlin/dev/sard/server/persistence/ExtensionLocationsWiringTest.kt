// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import dev.sard.server.onboarding.Database
import dev.sard.server.onboarding.TestPostgres
import org.flywaydb.core.Flyway
import org.springframework.boot.flyway.autoconfigure.FlywayMigrationStrategy
import org.springframework.boot.info.BuildProperties
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import java.nio.file.Files
import java.util.Properties
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertNotNull

private const val SETTING = "SARD_MIGRATE_EXTENSION_LOCATIONS"

/** Rule "Сервер не стартует, пока не применены миграции расширений": the start check reads the migrate setting. */
class ExtensionLocationsWiringTest {
    private val runner =
        ApplicationContextRunner()
            .withUserConfiguration(DatabaseVersionConfiguration::class.java)
            .withBean(BuildProperties::class.java, { BuildProperties(Properties().apply { setProperty("version", "0.1.0") }) })
            .withBean(DatabaseMismatchListener::class.java, { DatabaseMismatchListener { } })
            .withBean(RoleExcessListener::class.java, { RoleExcessListener { } })

    private fun flyway(database: Database) =
        Flyway
            .configure()
            .dataSource(database.url, database.user, database.password)
            .schemas("sard")
            .defaultSchema("sard")
            .load()

    private fun startWith(
        database: Database,
        vararg properties: String,
    ): Throwable? {
        var failure: Throwable? = null
        runner.withPropertyValues(*properties).run { ctx ->
            failure = runCatching { ctx.getBean(FlywayMigrationStrategy::class.java).migrate(flyway(database)) }.exceptionOrNull()
        }
        return failure
    }

    @Test
    fun `Расположение миграций расширений берётся из SARD_MIGRATE_EXTENSION_LOCATIONS окружения`() {
        val database = TestPostgres.newDatabase()
        val dir = Files.createTempDirectory("ext-wiring")
        Files.writeString(dir.resolve("V1__wired_events.sql"), "create table wired_events (id int);")

        val refusal = startWith(database, "$SETTING=filesystem:$dir")

        assertEquals(ExtensionMigrationsNotApplied::class, assertNotNull(refusal)::class)
        assertContains(assertNotNull(refusal.message), "wired_events")
    }

    @Test
    fun `Без настройки проверяется расположение по умолчанию, где миграций расширений нет`() {
        val database = TestPostgres.newDatabase()

        assertEquals(null, startWith(database))
    }
}
