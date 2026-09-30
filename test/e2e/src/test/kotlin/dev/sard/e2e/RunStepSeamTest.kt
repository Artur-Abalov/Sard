// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.fail

/**
 * S6a test 9, the seam with the real agent: a queued step of a run reaches the agent on its
 * Hello (S6a delivery), the agent answers with a StepResult, and that result reaches the
 * server's handler. The plugin is one the agent does not have, so the agent refuses the step
 * before running anything: REJECTED with "unknown plugin" (the executor checks the plugin
 * first, agent/internal/executor/command.go).
 *
 * Until S7 the server's result handler only logs ids and the status (LoggingInbound, debug):
 * that line is the evidence, and the step stays dispatched. The run rows are written by SQL
 * because the REST API for runs comes with S8b and a test hook on the server is ruled out
 * (ADR 0020); the rows are what `Runs.start` writes.
 */
class RunStepSeamTest {
    @Test
    fun `a queued step reaches the real agent on Hello and its rejection reaches the server`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard))
        val stepId = queueStep(UUID.fromString(agent.agentId))

        sard.track(AgentContainer.ALIAS, AgentContainer.of(sard, agent)).start()

        await("step $stepId dispatched") { statusOf(stepId) == "dispatched" }
        val handled = "agent ${agent.agentId} result of command $stepId: STEP_STATUS_REJECTED"
        await("'$handled' in the server log") { handled in sard.server.logs }
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
        val sard =
            SardEnvironment(
                // LoggingInbound, the result handler until S7, logs at debug.
                mapOf("LOGGING_LEVEL_DEV_SARD_SERVER_AGENTS_STREAM" to "DEBUG"),
            )

        private val TIMEOUT = Duration.ofSeconds(60)
        private val POLL = Duration.ofMillis(500)
    }
}
