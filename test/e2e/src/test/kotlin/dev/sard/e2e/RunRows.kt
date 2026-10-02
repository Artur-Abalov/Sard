// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.sql.Timestamp
import java.time.Instant
import java.util.UUID

/**
 * Run rows written by SQL, as `Runs.start` writes them. The REST API refuses what the seam tests
 * need (a source whose plugin the agent does not offer, S8b `unknown_plugin`), and a test hook on
 * the server is ruled out (ADR 0020). Shared by the seam tests of S6a and S7a.
 */
internal object RunRows {
    /** A source with [plugin] and [config], its manual run and the run's queued backup step; returns the step's id. */
    fun queueStep(
        sard: SardEnvironment,
        agentId: UUID,
        plugin: String,
        config: String = "{}",
    ): UUID {
        val (source, run, step) = List(3) { UUID.randomUUID() }
        val now = Timestamp.from(Instant.now())
        sard.database().use { connection ->
            fun insert(
                sql: String,
                vararg values: Any,
            ) = connection.prepareStatement(sql).use { statement ->
                values.forEachIndexed { i, value -> statement.setObject(i + 1, value) }
                statement.executeUpdate()
            }
            insert(
                """
                INSERT INTO sources (id, tenant_id, agent_id, name, plugin, config, repository_name,
                                     created_at, updated_at)
                VALUES (?, ?, ?, 'seam', ?, ?::jsonb, 'main', ?, ?)
                """.trimIndent(),
                source,
                EnrollmentTokens.DEFAULT_TENANT,
                agentId,
                plugin,
                config,
                now,
                now,
            )
            insert(
                "INSERT INTO runs (id, tenant_id, source_id, trigger, status, queued_at) VALUES (?, ?, ?, 'manual', 'queued', ?)",
                run,
                EnrollmentTokens.DEFAULT_TENANT,
                source,
                now,
            )
            insert(
                """
                INSERT INTO run_steps (id, tenant_id, run_id, ordinal, agent_id, source_id, plugin, action,
                                       repository_name, config, status, queued_at)
                VALUES (?, ?, ?, 0, ?, ?, ?, 'backup', 'main', ?::jsonb, 'queued', ?)
                """.trimIndent(),
                step,
                EnrollmentTokens.DEFAULT_TENANT,
                run,
                agentId,
                source,
                plugin,
                config,
                now,
            )
        }
        return step
    }

    fun statusOf(
        sard: SardEnvironment,
        stepId: UUID,
    ): String? =
        sard.database().use { connection ->
            connection.prepareStatement("SELECT status FROM run_steps WHERE id = ?").use { query ->
                query.setObject(1, stepId)
                query.executeQuery().use { if (it.next()) it.getString(1) else null }
            }
        }
}
