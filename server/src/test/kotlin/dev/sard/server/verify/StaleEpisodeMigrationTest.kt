// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.database.MigrationSandbox
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals

private const val V6 = "202610261200"

/**
 * Migration V202610261200 and the server of the version before it (V4) still running on the migrated database, as in
 * a rollback of the binary alone: its insert of a stale episode names neither the threshold nor the zone, and must
 * still succeed, with what that server implied, seven days and UTC.
 */
class StaleEpisodeMigrationTest {
    private val sandbox = MigrationSandbox()
    private val connection = sandbox.connection
    private val jdbc = JdbcTemplate(sandbox.dataSource)

    @AfterTest
    fun `close the connection`() {
        connection.close()
    }

    @Test
    fun `a stale episode opened by the previous server version gets seven days and UTC`() {
        sandbox.flyway(V6).migrate()
        jdbc.execute("set session_replication_role = replica")
        val now = Timestamp.from(Instant.parse("2026-10-26T12:00:00Z"))

        jdbc.update(
            "insert into verification_stale_episodes (id, tenant_id, source_id, counted_from, opened_at) " +
                "values (?, ?, ?, ?, ?)",
            UUID.randomUUID(),
            UUID.randomUUID(),
            UUID.randomUUID(),
            now,
            now,
        )

        val row = jdbc.queryForMap("select threshold_days, timezone from verification_stale_episodes")
        assertEquals(listOf<Any?>(7, "UTC"), listOf(row["threshold_days"], row["timezone"]))
    }
}
