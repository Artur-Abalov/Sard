// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.database.MigrationSandbox
import org.springframework.dao.DataIntegrityViolationException
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

private const val BEFORE = "202610271000"
private const val PARTIAL_CHECK = "snapshots_incomplete_reason_partial_check"
private const val SCHEMA = "sard"
private val T0 = Timestamp.from(Instant.parse("2026-10-28T12:00:00Z"))

/** Migration V202610281000 (PL7-2): the reason a snapshot is incomplete; snapshots from before it say unknown. */
class PartialReasonMigrationTest {
    private val sandbox = MigrationSandbox()
    private val connection = sandbox.connection
    private val jdbc = JdbcTemplate(sandbox.dataSource)

    @AfterTest
    fun `close the connection`() {
        connection.close()
    }

    private fun snapshot(
        snapshotId: String,
        partial: Boolean,
    ) = jdbc.update(
        "insert into snapshots (id, tenant_id, source_id, step_id, agent_id, repository_name, repository_id, " +
            "snapshot_id, total_bytes, added_bytes, created_at, partial) " +
            "values (?, ?, ?, ?, ?, 'main', 'r', ?, 1, 1, ?, ?)",
        UUID.randomUUID(),
        UUID.randomUUID(),
        UUID.randomUUID(),
        UUID.randomUUID(),
        UUID.randomUUID(),
        snapshotId,
        T0,
        partial,
    )

    private fun reasons(): Map<String, String?> =
        jdbc.queryForList("select snapshot_id, incomplete_reason from snapshots").associate {
            it["snapshot_id"] as String to it["incomplete_reason"] as String?
        }

    private fun migrated() {
        sandbox.flyway(BEFORE).migrate()
        jdbc.execute("set search_path to $SCHEMA")
        jdbc.execute("set session_replication_role = replica")
        snapshot("whole", partial = false)
        snapshot("partial", partial = true)
        sandbox.flyway().migrate()
        jdbc.execute("set search_path to $SCHEMA")
    }

    @Test
    fun `a partial snapshot of before the reason says unknown, a whole one says nothing`() {
        migrated()

        assertEquals(mapOf("whole" to null, "partial" to "unknown"), reasons())
    }

    @Test
    fun `the database refuses a reason outside the three`() {
        migrated()

        val refused =
            assertFailsWith<DataIntegrityViolationException> {
                jdbc.update("update snapshots set incomplete_reason = 'other' where snapshot_id = 'partial'")
            }
        assertTrue("snapshots_incomplete_reason_check" in refused.message.orEmpty(), refused.message)
        jdbc.update("update snapshots set incomplete_reason = 'snapshot_set_incomplete' where snapshot_id = 'partial'")
        assertEquals("snapshot_set_incomplete", reasons()["partial"])
    }

    private fun assertRefusedByPartialCheck(sql: String) {
        val refused = assertFailsWith<DataIntegrityViolationException> { jdbc.update(sql) }
        assertTrue(PARTIAL_CHECK in refused.message.orEmpty(), refused.message)
    }

    @Test
    fun `the database refuses a partial snapshot without a reason and a whole one with a reason`() {
        migrated()

        assertRefusedByPartialCheck("update snapshots set incomplete_reason = null where snapshot_id = 'partial'")
        assertRefusedByPartialCheck("update snapshots set incomplete_reason = 'unknown' where snapshot_id = 'whole'")
    }
}
