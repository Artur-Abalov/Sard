// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
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
        val stepId = RunRows.queueStep(sard, UUID.fromString(agent.agentId), PLUGIN)

        sard.track(AgentContainer.ALIAS, AgentContainer.of(agent)).start()

        await("step $stepId rejected") { RunRows.statusOf(sard, stepId) == "rejected" }
        assertEquals(listOf("unknown plugin \"$PLUGIN\"", "failed"), messageAndRunStatus(stepId))
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
