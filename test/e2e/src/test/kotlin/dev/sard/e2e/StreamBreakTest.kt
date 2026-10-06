// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import dev.sard.e2e.Interruptions.Direction
import org.junit.jupiter.api.Timeout
import org.junit.jupiter.api.extension.RegisterExtension
import org.testcontainers.containers.GenericContainer
import java.time.Duration
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * T3: the link between agent and server breaks, or the agent restarts, while an `e2e-slow`
 * backup runs, and the backup still ends once: never a second backup, never a lost or doubled
 * result ([ExactlyOnce]: `restic backup` runs, tagged snapshots, RunFinished).
 *
 * The lost window of this stand is long ([LOST_WINDOW]): the agent's reconnect backoff (1 s
 * doubling to 60 s, full jitter, transport.go) may take tens of seconds after the server is back,
 * and a step must not be lost for that. The short window is for the cases where the agent does
 * not come back ([StepLossTest]). Each test enrolls its own agent; the server is shared,
 * restarted by two of them.
 */
@Timeout(value = 5, unit = TimeUnit.MINUTES)
class StreamBreakTest {
    private val rows = StepRows(sard)

    /** Case 1: a network cut longer than the server's offline threshold, shorter than the lost window. */
    @Test
    fun `a network cut shorter than the lost window leaves one succeeded step`() {
        val stand = T3Agent.start(sard, "cut")
        val started = stand.backup(STEP_SECONDS)

        val cutAt = rows.now()
        Interruptions.cut(sard, stand.container)
        // The server closes the silent session after OFFLINE and starts the step's lost window.
        val deadline = Await.value("the lost deadline the server sets when the session ends", OFFLINE.multipliedBy(3)) { rows.lostDeadline(started.stepId) }
        val cutFor = Duration.between(cutAt, rows.now())
        Interruptions.reconnect(sard, stand.container, stand.hostname)

        assertTrue(cutFor < LOST_WINDOW, "the cut lasted $cutFor, not shorter than the lost window $LOST_WINDOW")
        val window = Duration.between(cutAt, deadline)
        assertTrue(window >= LOST_WINDOW && window <= LOST_WINDOW + OFFLINE.multipliedBy(2), "lost deadline $window after the cut, want $LOST_WINDOW + up to the offline threshold")
        val step = Backups.awaitFinished(sard, started.stepId, STEP_TIMEOUT)
        assertEquals("succeeded", step.status, step.message)
        assertTrue(rows.finishedAt(started.stepId) > cutAt, "the step finished before the cut")
        assertTrue(ExactlyOnce.count(stand.once.agentLog(), "connection to the server lost") >= 1, "the agent did not notice the cut")
        assertSentOnce(stand, started)
        stand.once.assertOne(started)
    }

    /** Case 2: `docker restart` of the server mid-backup. */
    @Test
    fun `a server restart mid-backup records the result once and never sends the step again`() {
        serverGoesAway("restart") { sard.restartServer() }
    }

    /** Case 3: the server's container is replaced, its CA and database on volumes (T3s). */
    @Test
    fun `a recreated server mid-backup records the result once and never sends the step again`() {
        serverGoesAway("recreate") { sard.recreateServer() }
    }

    /**
     * Case 4 (D13, FXa): the agent's process is killed while the step runs and started again. Its
     * journal holds the step, so it reports it FAILED with the D13 message right after its Hello,
     * long before the lost window would; restic is not run again. The source is free: its next
     * run succeeds.
     */
    @Test
    fun `an agent killed mid-step fails the step at once with the D13 message and the next run succeeds`() {
        agentStopsMidStep("killed", INTERRUPTED) { container ->
            Interruptions.kill(container)
            Interruptions.start(container)
        }
    }

    /**
     * Case 4, a graceful restart (`docker restart`: SIGTERM, as `systemctl restart`): the agent
     * stops restic itself and saves the step FAILED with its shutdown message (A4), which it sends
     * after the Hello. The same guarantees as a kill; only the message differs (OQ-139).
     */
    @Test
    fun `an agent restarted gracefully mid-step fails the step at once and the next run succeeds`() {
        agentStopsMidStep("restarted", SHUTTING_DOWN) { container -> Interruptions.restart(container) }
    }

    /** Case 4: [restart] takes the agent down while restic backs the step up and brings it back. */
    private fun agentStopsMidStep(
        name: String,
        message: String,
        restart: (GenericContainer<*>) -> Unit,
    ) {
        val stand = T3Agent.start(sard, name)
        val started = stand.backup(STEP_SECONDS)
        Await.until("restic backup of step ${started.stepId}") { stand.once.resticBackups(started.stepId) == 1 }

        val restartAt = rows.now()
        restart(stand.container)
        val step = Backups.awaitFinished(sard, started.stepId, STEP_TIMEOUT)

        assertEquals("failed", step.status, step.message)
        assertEquals(message, step.message)
        val tookFor = Duration.between(restartAt, rows.finishedAt(started.stepId))
        assertTrue(tookFor < LOST_WINDOW.dividedBy(2), "the failure came $tookFor after the restart: the server waited for the lost window")
        assertEquals("failed", rows.runStatus(started.runId))
        assertSentOnce(stand, started)
        stand.once.assertOne(started, snapshots = 0)

        assertNextRunSucceeds(stand, started)
    }

    /**
     * Case 5, the command reached the journal: the agent accepted the step and started restic,
     * but its ACCEPTED never reached the server (iptables drops the agent's data), so the step is
     * still dispatched when the agent's process is killed and started again. After the Hello the server sends it again (it is
     * absent from the Hello); either way the agent answers with the journaled FAILED (D13) and
     * runs nothing a second time.
     */
    @Test
    fun `an agent killed with a dispatched step in its journal fails the step without a second backup`() {
        val stand = T3Agent.start(sard, "journaled")
        Interruptions.block(stand.container, Direction.TO_SERVER)
        val started = stand.backupNoWait(STEP_SECONDS)
        Await.until("restic backup of step ${started.stepId}") { stand.once.resticBackups(started.stepId) == 1 }
        assertEquals("dispatched", RunRows.statusOf(sard, started.stepId), "the agent's ACCEPTED got through the block")

        Interruptions.kill(stand.container)
        Interruptions.start(stand.container)
        // The new start gives the container a new network namespace without the rules; flush anyway.
        Interruptions.unblock(stand.container)
        val step = Backups.awaitFinished(sard, started.stepId, STEP_TIMEOUT)

        assertEquals("failed", step.status, step.message)
        assertEquals(INTERRUPTED, step.message)
        assertEquals(1, stand.once.agentLines("step accepted", started.stepId), "the agent accepted the step again")
        assertEquals(1, stand.once.agentLines("step started", started.stepId), "the agent started the step again")
        stand.once.assertOne(started, snapshots = 0)

        assertNextRunSucceeds(stand, started)
    }

    /**
     * Case 5, the command did not reach the journal: the agent is off the network when the server
     * sends the step (its session is not closed yet, so the step is dispatched into a dead
     * connection), restarts without it, and comes back. The server sends the step again after the
     * Hello, and it runs once.
     */
    @Test
    fun `an agent restarted before a dispatched step reached it runs the step sent again once`() {
        val stand = T3Agent.start(sard, "unjournaled")
        Interruptions.cut(sard, stand.container)
        val started = stand.backupNoWait(STEP_SECONDS)
        stand.awaitStatus(started.stepId, "dispatched")

        Interruptions.restart(stand.container)
        Interruptions.reconnect(sard, stand.container, stand.hostname)
        val step = Backups.awaitFinished(sard, started.stepId, STEP_TIMEOUT)

        assertEquals("succeeded", step.status, step.message)
        assertEquals(1, ExactlyOnce.count(sard.serverLogs(), "step ${started.stepId} sent again"), "sends again of the step")
        assertEquals(1, stand.once.agentLines("step accepted", started.stepId), "the agent accepted the step more than once")
        assertEquals(0, ExactlyOnce.count(stand.once.agentLog(), "interrupted steps reported as failed"), "the step reached the journal before the restart")
        stand.once.assertOne(started)
    }

    /**
     * Case 6: the backup ends and its result reaches the server, but the ResultAck does not reach
     * the agent (iptables drops what the server sends). The agent drops the dead stream, sends the
     * result again on the next one, the server records nothing new and acknowledges, and the agent
     * keeps the tombstone: it never sends that result again.
     */
    @Test
    fun `a result whose ack is lost is sent again, recorded once, and then no more`() {
        val stand = T3Agent.start(sard, "ack")
        val started = stand.backup(SHORT_STEP_SECONDS)
        val drops = ExactlyOnce.count(stand.once.agentLog(), "connection to the server lost")

        Interruptions.block(stand.container, Direction.TO_AGENT)
        Await.until("the server's record of step ${started.stepId}", STEP_TIMEOUT) { RunRows.statusOf(sard, started.stepId) == "succeeded" }
        Await.until("the agent dropping the stream without an ack", RECONNECT_TIMEOUT) {
            ExactlyOnce.count(stand.once.agentLog(), "connection to the server lost") > drops
        }
        assertEquals(1, stand.once.agentLines("result sent", started.stepId), "results sent before the stream dropped")
        assertEquals(0, stand.once.agentLines("result acknowledged", started.stepId), "an ack got through the block")
        Interruptions.unblock(stand.container)

        Await.until("the ack of the repeated result", RECONNECT_TIMEOUT) { stand.once.agentLines("result acknowledged", started.stepId) == 1 }
        assertEquals(2, stand.once.agentLines("result sent", started.stepId), "results sent in all")
        assertEquals(1, ExactlyOnce.count(sard.serverLogs(), "repeated the result of step ${started.stepId}"), "repeats the server saw")
        assertTrue(stand.holds("acked", started.stepId), "no tombstone of ${started.stepId}: the agent would send it again")
        assertFalse(stand.holds("results", started.stepId), "the agent still holds the result of ${started.stepId} to send")
        assertEquals("succeeded", RunRows.statusOf(sard, started.stepId))
        stand.once.assertOne(started)
    }

    /** Cases 2 and 3: [away] takes the server down while the step runs and brings it back. */
    private fun serverGoesAway(
        name: String,
        away: () -> Unit,
    ) {
        val stand = T3Agent.start(sard, name)
        val started = stand.backup(STEP_SECONDS)

        val awayAt = rows.now()
        away()
        Await.until("Register of the agent after the server came back", RECONNECT_TIMEOUT) { rows.registeredAfter(stand.agent.agentId, awayAt) }
        val step = Backups.awaitFinished(sard, started.stepId, STEP_TIMEOUT)

        assertEquals("succeeded", step.status, step.message)
        assertEquals(0, ExactlyOnce.count(sard.serverLogs(), "step ${started.stepId} sent again"), "the server sent the step again")
        assertSentOnce(stand, started)
        stand.once.assertOne(started)
    }

    /** The agent took the step once and never got it again (A4 dedup would log a repeated command). */
    private fun assertSentOnce(
        stand: T3Agent,
        started: Backups.Started,
    ) {
        assertEquals(1, stand.once.agentLines("step accepted", started.stepId), "the agent accepted the step more than once")
        assertEquals(0, stand.once.agentLines("repeated command", started.stepId), "the server sent the step again")
    }

    /** The source is not blocked: a new run of it is accepted and its backup succeeds. */
    private fun assertNextRunSucceeds(
        stand: T3Agent,
        previous: Backups.Started,
    ) {
        val next = Backups.run(sard, previous.sourceId)
        val step = Backups.awaitFinished(sard, next.stepId, STEP_TIMEOUT)
        assertEquals("succeeded", step.status, step.message)
        stand.once.assertOne(next)
    }

    companion object {
        private val HEARTBEAT: Duration = Duration.ofSeconds(5)
        private const val LOST_AFTER_HEARTBEATS = 20L

        /** The server closes a session silent this long (3 heartbeats, application.yaml). */
        private val OFFLINE: Duration = HEARTBEAT.multipliedBy(3)

        /** A step in flight without its agent is lost after this (S6a, FXs). */
        private val LOST_WINDOW: Duration = HEARTBEAT.multipliedBy(LOST_AFTER_HEARTBEATS)

        /** The D13 failure the agent reports for a step its killed process left (executor/command.go). */
        const val INTERRUPTED = "interrupted: agent restarted before the step finished"

        /** The failure the agent saves for a step it stops on SIGTERM (executor/command.go, A4). */
        const val SHUTTING_DOWN = "agent is shutting down"

        @JvmField
        @RegisterExtension
        val sard =
            SardEnvironment(
                mapOf(
                    "SARD_AGENT_HEARTBEAT_INTERVAL" to "${HEARTBEAT.seconds}s",
                    "SARD_AGENT_STREAM_CHECKINTERVAL" to "1s",
                    "SARD_RUN_DISPATCH_LOSTAFTERHEARTBEATS" to "$LOST_AFTER_HEARTBEATS",
                    // "agent … repeated the result of step …" (StepResultReceiver) is a debug line.
                    "LOGGING_LEVEL_DEV_SARD_SERVER_AGENTS_RESULTS" to "DEBUG",
                ),
            )

        /** Long enough for a cut, a restart or a server restart to land mid-step. */
        private const val STEP_SECONDS = 30

        /**
         * Case 6: the result must be sent before the blocked stream dies. gRPC drops a connection
         * that reads nothing for the keepalive time plus timeout (30 + 10 s, transport.go), at the
         * earliest 10 s after the block; the step ends a few seconds after it.
         */
        private const val SHORT_STEP_SECONDS = 4

        private val STEP_TIMEOUT: Duration = Duration.ofMinutes(2)
        private val RECONNECT_TIMEOUT: Duration = Duration.ofMinutes(2)
    }
}
