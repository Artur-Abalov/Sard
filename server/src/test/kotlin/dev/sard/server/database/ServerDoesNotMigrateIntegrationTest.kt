// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import dev.sard.server.onboarding.Installation
import dev.sard.server.onboarding.TestPostgres
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** Rule "sard_app только читает и пишет строки", scenario "Сервер не применяет миграции сам". */
class ServerDoesNotMigrateIntegrationTest {
    @TempDir
    lateinit var tmp: Path

    private fun Throwable.messages() = generateSequence(this) { it.cause }.mapNotNull { it.message }.joinToString("\n")

    @Test
    fun `Сервер не применяет миграции сам`() {
        val database = TestPostgres.newDatabase(migrated = false)

        val (failure, log) = Installation(tmp, database).startFailing()

        val text = failure.messages() + log.joinToString("\n") { it.text }
        assertTrue("MIGRATIONS_NOT_APPLIED" in text, text.take(2000))
        assertTrue("migrate" in text && "docs/operator/16-postgresql.md" in text, text.take(2000))
        val tables =
            TestPostgres.databases().query(
                "select count(*) from pg_tables where schemaname = 'sard'",
                database.url.substringAfterLast('/'),
            ) {
                it.getInt(1)
            }
        assertEquals(0, tables)
    }
}
