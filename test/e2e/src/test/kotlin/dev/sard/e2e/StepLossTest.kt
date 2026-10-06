// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.Timeout
import org.junit.jupiter.api.extension.RegisterExtension
import java.time.Duration
import java.time.Instant
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * T3: a running step whose agent does not come back with it is lost after the lost window, its
 * run fails, and its source is free again (Д1, Д2, FXs). The window of this stand is short
 * ([LOST_WINDOW], 10 s): heartbeat 5 s × 2, checked every second.
 *
 * "Exactly one" is the same as in [StreamBreakTest]: one `restic backup` run, no snapshot of the
 * interrupted step, one RunFinished.
 */
@Timeout(value = 5, unit = TimeUnit.MINUTES)
class StepLossTest {
    private val rows = StepRows(sard)

    /** Case 7: the agent stops (`docker stop`) mid-step and never returns. */
    @Test
    fun `a step whose agent stopped for good is lost after the window and frees its source`() {
        val stand = T3Agent.start(sard, "gone")
        val started = runningBackup(stand)

        val stopAt = rows.now()
        Interruptions.stop(stand.container)
        val step = Backups.awaitFinished(sard, started.stepId, LOSS_TIMEOUT)

        assertLost(step)
        val after = Duration.between(stopAt, rows.finishedAt(started.stepId))
        assertTrue(after >= LOST_WINDOW.minus(SLACK) && after <= LOST_WINDOW + OFFLINE + SLACK, "lost $after after the agent stopped, want $LOST_WINDOW (up to $OFFLINE more if the session was not closed)")
        assertFailedOnce(stand, started)
        assertSourceFree(started)
    }

    /**
     * Case 8: the agent restarts again and again, faster than the window, and each time comes
     * back without the step: its process is killed (on SIGTERM it would save the step's failure)
     * and the executor's journal erased before it starts again (a host whose disk was lost). Each Hello without the step must not push the deadline: the step is lost
     * by the first one (Д2).
     */
    @Test
    fun `an agent restarting faster than the window without the step loses it by the first deadline`() {
        val stand = T3Agent.start(sard, "flapping")
        val started = runningBackup(stand)

        val firstKill = rows.now()
        var restarts = 0
        var longest = Duration.ZERO
        while (RunRows.statusOf(sard, started.stepId) == "running" && rows.now() < firstKill + LOST_WINDOW.multipliedBy(3)) {
            val cycleAt = rows.now()
            val hellos = ExactlyOnce.count(stand.once.agentLog(), "hello sent")
            Interruptions.kill(stand.container)
            Interruptions.erase(stand.container, stand.agent.host, T3Agent.JOURNAL_DIR)
            Interruptions.start(stand.container)
            Await.until("the agent's Hello after restart ${restarts + 1}") { ExactlyOnce.count(stand.once.agentLog(), "hello sent") > hellos }
            restarts++
            longest = maxOf(longest, Duration.between(cycleAt, rows.now()))
        }
        val step = Backups.awaitFinished(sard, started.stepId, LOSS_TIMEOUT)

        assertLost(step)
        assertTrue(restarts >= 2, "only $restarts restarts before the step was lost: the deadline was not tested against a later Hello")
        assertTrue(longest < LOST_WINDOW, "a restart took $longest, not faster than the window $LOST_WINDOW")
        val after = Duration.between(firstKill, rows.finishedAt(started.stepId))
        assertTrue(after <= LOST_WINDOW + SLACK, "lost $after after the first kill, want $LOST_WINDOW: a later Hello moved the deadline")
        assertEquals(0, ExactlyOnce.count(stand.once.agentLog(), "interrupted steps reported as failed"), "the erased journal still reported the step")
        assertFailedOnce(stand, started)

        val next = Backups.run(sard, started.sourceId)
        val nextStep = Backups.awaitFinished(sard, next.stepId, LOSS_TIMEOUT)
        assertEquals("succeeded", nextStep.status, nextStep.message)
        stand.once.assertOne(next)
    }

    /**
     * Case 9: the step runs, its agent stops for good, and the server restarts. The deadline the
     * session's end set passes while the server is down; the start gives every step in flight a
     * fresh window (FXs, В2), so the step is lost a window after the new server started, not at
     * once.
     */
    @Test
    fun `a running step without its agent across a server restart is lost a window after the start`() {
        val stand = T3Agent.start(sard, "orphan")
        val started = runningBackup(stand)

        Interruptions.stop(stand.container)
        Await.until("the lost deadline of the closed session") { rows.lostDeadline(started.stepId) != null }
        sard.restartServer()
        val healthyAt = rows.now()
        val serverStartedAt = Instant.parse(sard.server.currentContainerInfo.state.startedAt)
        val step = Backups.awaitFinished(sard, started.stepId, LOSS_TIMEOUT)

        assertLost(step)
        val lostAt = rows.finishedAt(started.stepId)
        assertTrue(lostAt >= serverStartedAt + LOST_WINDOW.minus(SLACK), "lost at $lostAt, before the window from the server's start $serverStartedAt")
        assertTrue(lostAt <= healthyAt + LOST_WINDOW + SLACK, "lost at $lostAt, later than a window after the server was healthy at $healthyAt")
        assertFailedOnce(stand, started)
        assertSourceFree(started)
    }

    /** Starts a long backup and waits until restic runs it, so that the step is truly mid-backup. */
    private fun runningBackup(stand: T3Agent): Backups.Started {
        val started = stand.backup(STEP_SECONDS)
        Await.until("restic backup of step ${started.stepId}") { stand.once.resticBackups(started.stepId) == 1 }
        return started
    }

    private fun assertLost(step: Backups.Step) {
        assertEquals("lost", step.status, step.message)
        assertEquals(LOST_MESSAGE, step.message)
    }

    /** One restic run, no snapshot, one RunFinished; the run failed. */
    private fun assertFailedOnce(
        stand: T3Agent,
        started: Backups.Started,
    ) {
        assertEquals("failed", rows.runStatus(started.runId))
        stand.once.assertOne(started, snapshots = 0)
    }

    /** The source takes a new run (201) although its agent is away: the run waits, queued. */
    private fun assertSourceFree(started: Backups.Started) {
        val next = Backups.run(sard, started.sourceId)
        assertEquals("queued", RunRows.statusOf(sard, next.stepId))
        assertEquals("queued", rows.runStatus(next.runId))
    }

    companion object {
        private val HEARTBEAT: Duration = Duration.ofSeconds(5)

        /** The server closes a session silent this long (3 heartbeats, application.yaml). */
        private val OFFLINE: Duration = HEARTBEAT.multipliedBy(3)

        /** lost-after-heartbeats (2, the default) × heartbeat. */
        private val LOST_WINDOW: Duration = HEARTBEAT.multipliedBy(2)

        /** The check interval (1 s), the clocks' second resolution, the database round trips. */
        private val SLACK: Duration = Duration.ofSeconds(3)

        /** The server's message for a lost step (runs/StepTransitions.kt). */
        private const val LOST_MESSAGE = "agent lost the step"

        @JvmField
        @RegisterExtension
        val sard =
            SardEnvironment(
                mapOf(
                    "SARD_AGENT_HEARTBEAT_INTERVAL" to "${HEARTBEAT.seconds}s",
                    "SARD_AGENT_STREAM_CHECKINTERVAL" to "1s",
                ),
            )

        /** Long enough to outlast every interruption: the step must never finish on its own. */
        private const val STEP_SECONDS = 30

        private val LOSS_TIMEOUT: Duration = Duration.ofMinutes(2)
    }
}
