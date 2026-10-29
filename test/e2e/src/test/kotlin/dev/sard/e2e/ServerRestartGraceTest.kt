// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.Timeout
import org.junit.jupiter.api.extension.RegisterExtension
import java.time.Duration
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals

/**
 * J1d: an ordinary server restart in the middle of a backup does not lose the step. The lost window of
 * this stand is short ([LOST_WINDOW], 10 s), shorter than the agent's worst reconnect pause (60 s, transport.go):
 * without the start grace (`sard.run.dispatch.start-grace`, 2 min by default) a slow reconnect would
 * make the step `lost` although the agent never stopped (OQ-138).
 */
@Timeout(value = 5, unit = TimeUnit.MINUTES)
class ServerRestartGraceTest {
    private val rows = StepRows(sard)

    @Test
    fun `a server restart mid-backup does not lose the step and the run succeeds`() {
        val stand = T3Agent.start(sard, "grace")
        val started = stand.backup(STEP_SECONDS)

        val awayAt = rows.now()
        sard.restartServer()
        Await.until("Register of the agent after the server came back", Duration.ofMinutes(2)) { rows.registeredAfter(stand.agent.agentId, awayAt) }
        val step = Backups.awaitFinished(sard, started.stepId, Duration.ofMinutes(2))

        assertEquals("succeeded", step.status, step.message)
        assertEquals("succeeded", rows.runStatus(started.runId))
        assertEquals(0, ExactlyOnce.count(sard.serverLogs(), "step ${started.stepId} lost"), "the server lost the step")
        stand.once.assertOne(started)
    }

    companion object {
        private val HEARTBEAT: Duration = Duration.ofSeconds(5)

        /** lost-after-heartbeats (2, the default) × heartbeat. */
        private val LOST_WINDOW: Duration = HEARTBEAT.multipliedBy(2)

        /** Long enough for the restart to land mid-step. */
        private const val STEP_SECONDS = 30

        @JvmField
        @RegisterExtension
        val sard =
            SardEnvironment(
                mapOf(
                    "SARD_AGENT_HEARTBEAT_INTERVAL" to "${HEARTBEAT.seconds}s",
                    "SARD_AGENT_STREAM_CHECKINTERVAL" to "1s",
                    "SARD_RUN_START_GRACE" to "2m",
                ),
            )
    }
}
