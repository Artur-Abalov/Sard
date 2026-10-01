// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.test.fail

/**
 * A7c strategy 4, the real agent and restic: a files step whose repository path holds the value
 * of one of the agent's secrets. The repository does not exist, so restic fails and prints the
 * path on stderr; the agent masks the value before the lines leave the host (ADR 0031). The
 * server's `step_logs` carry the marker and never the value, nor does the step's message.
 */
class StepLogRedactionTest {
    @Test
    fun `a secret restic prints reaches step_logs as REDACTED`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard))
        val stepId = RunRows.queueStep(sard, UUID.fromString(agent.agentId), "files", """{"paths":["/etc/sard/agent.yaml"]}""")
        val files = mapOf("/etc/sard/token" to "$SECRET\n", "/etc/sard/main.pass" to "e2e-password\n")
        sard.track(AgentContainer.ALIAS, AgentContainer.of(sard, agent, LOCAL, files)).start()

        await("step $stepId failed") { RunRows.statusOf(sard, stepId) == "failed" }
        await("a masked line of step $stepId") { logsOf(stepId).any { MARKER in it } }

        val logs = logsOf(stepId)
        assertTrue(logs.none { SECRET in it }, "the value reached step_logs: $logs")
        assertTrue(logs.any { "repo-$MARKER" in it }, "no line names the masked repository: $logs")
        assertFalse(SECRET in messageOf(stepId).orEmpty(), "the value reached the step's message")
    }

    private fun logsOf(stepId: UUID): List<String> =
        sard.database().use { connection ->
            connection.prepareStatement("SELECT text FROM step_logs WHERE step_id = ? ORDER BY seq").use { query ->
                query.setObject(1, stepId)
                query.executeQuery().use { rows -> generateSequence { if (rows.next()) rows.getString(1) else null }.toList() }
            }
        }

    private fun messageOf(stepId: UUID): String? =
        sard.database().use { connection ->
            connection.prepareStatement("SELECT message FROM run_steps WHERE id = ?").use { query ->
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
        private const val SECRET = "e2e-hunter2-very-secret"
        private const val MARKER = "[REDACTED]"

        /** A repository that does not exist under the agent's state dir; its path holds the secret. */
        private val LOCAL =
            """
            repositories:
              - name: main
                url: /var/lib/sard-agent/repo-$SECRET
                password_file: /etc/sard/main.pass
            secrets:
              token: /etc/sard/token
            """.trimIndent()

        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val TIMEOUT = Duration.ofSeconds(60)
        private val POLL = Duration.ofMillis(500)
    }
}
