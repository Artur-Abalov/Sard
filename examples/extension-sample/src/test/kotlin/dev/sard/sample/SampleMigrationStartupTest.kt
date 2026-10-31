// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import dev.sard.server.SardServerApplication
import org.springframework.boot.WebApplicationType
import org.springframework.boot.builder.SpringApplicationBuilder
import org.testcontainers.postgresql.PostgreSQLContainer
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

/** The migration of the sample is not applied: the server refuses to start (server-core-extensions.feature, @startup). */
class SampleMigrationStartupTest {
    private fun start(container: PostgreSQLContainer) =
        SpringApplicationBuilder(SardServerApplication::class.java)
            .web(WebApplicationType.NONE)
            .properties(
                "SARD_DB_URL=${container.jdbcUrl}",
                "SARD_DB_USER=sard_app",
                "SARD_DB_PASSWORD=sard_app-password",
                "spring.grpc.server.port=0",
                "sard.pki.dir=${Files.createTempDirectory("sample-pki")}",
            ).run()

    private fun messages(failure: Throwable) = generateSequence(failure) { it.cause }.map { it.message.orEmpty() }.toList()

    @Test
    fun `Неприменённая миграция расширения останавливает старт и отказ ничего не меняет в базе`() {
        val container = SampleDatabase.container().also { it.start() }
        try {
            SampleDatabase.bootstrap(container)
            SampleDatabase.migrate(container, extensions = emptyList())

            val failure = assertFailsWith<Throwable> { start(container).close() }

            val text = messages(failure).firstOrNull { it.startsWith("Database startup refused: EXTENSION_MIGRATIONS_NOT_APPLIED:") }
            assertTrue(text != null, messages(failure).toString())
            assertContains(text, "sard.flyway_schema_history_extensions")
            assertContains(text, "sample_events")
            assertContains(text, "migrate")
            assertContains(text, "docs/operator/16-postgresql.md")
            val tables =
                SampleDatabase.query(
                    container,
                    "select count(*) from information_schema.tables where table_schema = 'sard' and " +
                        "table_name in ('sample_events', 'flyway_schema_history_extensions')",
                ) { it.getInt(1) }
            assertTrue(tables == 0, "the refusal changed the database")
        } finally {
            container.stop()
        }
    }
}
