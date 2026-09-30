// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.fail

/**
 * S6a test 9, the seam with the real agent: a queued step of a run reaches the agent on its
 * Hello (S6a delivery), the agent answers with a StepResult, and that result reaches the
 * server's handler. The plugin is one the agent does not have, so the agent refuses the step
 * before running anything: REJECTED with "unknown plugin" (the executor checks the plugin
 * first, agent/internal/executor/command.go).
 *
 * Since S7a the result is recorded: the step is rejected with the agent's message and its run
 * failed. The run rows are written by SQL because the REST API for runs comes with S8b and a
 * test hook on the server is ruled out (ADR 0020); the rows are what `Runs.start` writes.
 */
class RunStepSeamTest {
    @Test
    fun `a queued step reaches the real agent on Hello and its rejection reaches the server`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard))
        val stepId = queueStep(UUID.fromString(agent.agentId))

        sard.track(AgentContainer.ALIAS, AgentContainer.of(sard, agent)).start()

        await("step $stepId rejected") { statusOf(stepId) == "rejected" }
        assertEquals(listOf("unknown plugin \"$PLUGIN\"", "failed"), messageAndRunStatus(stepId))
    }

    /** A source whose plugin the agent lacks, its manual run and the run's queued backup step. */
    private fun queueStep(agentId: UUID): UUID {
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
                VALUES (?, ?, ?, 'seam', '$PLUGIN', '{}'::jsonb, 'main', ?, ?)
                """.trimIndent(),
                source,
                EnrollmentTokens.DEFAULT_TENANT,
                agentId,
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
                VALUES (?, ?, ?, 0, ?, ?, '$PLUGIN', 'backup', 'main', '{}'::jsonb, 'queued', ?)
                """.trimIndent(),
                step,
                EnrollmentTokens.DEFAULT_TENANT,
                run,
                agentId,
                source,
                now,
            )
        }
        return step
    }

    private fun statusOf(stepId: UUID): String? =
        sard.database().use { connection ->
            connection.prepareStatement("SELECT status FROM run_steps WHERE id = ?").use { query ->
                query.setObject(1, stepId)
                query.executeQuery().use { if (it.next()) it.getString(1) else null }
            }
        }

    private fun messageAndRunStatus(stepId: UUID): List<String?> =
        sard.database().use { connection ->
            val sql = "SELECT s.message, r.status FROM run_steps s JOIN runs r ON r.id = s.run_id WHERE s.id = ?"
            connection.prepareStatement(sql).use { query ->
                query.setObject(1, stepId)
                query.executeQuery().use { if (it.next()) listOf(it.getString(1), it.getString(2)) else emptyList() }
            }
        }

    private fun await(
        what: String,
        condition: () -> Boolean,
    ) {
        val deadline = Instant.now() + TIMEOUT
        while (Instant.now() < deadline) {
            if (condition()) return
            Thread.sleep(POLL.toMillis())
        }
        fail("no $what within $TIMEOUT")
    }

    companion object {
        /** A plugin no agent has (built in are postgresql, mysql, files and network). */
        private const val PLUGIN = "absent"

        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val TIMEOUT = Duration.ofSeconds(60)
        private val POLL = Duration.ofMillis(500)
    }
}
