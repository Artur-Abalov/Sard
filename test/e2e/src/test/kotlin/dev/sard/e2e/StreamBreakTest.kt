// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import dev.sard.e2e.Interruptions.Direction
import org.junit.jupiter.api.Timeout
import org.junit.jupiter.api.extension.RegisterExtension
import org.testcontainers.containers.GenericContainer
import java.security.MessageDigest
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.time.OffsetDateTime
import java.util.HexFormat
import java.util.UUID
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * T3: the link between agent and server breaks while an `e2e-slow` backup runs, and the backup
 * still ends once: one `restic backup`, one snapshot, one result, one RunFinished ([ExactlyOnce]).
 *
 * The lost window of this stand is long ([LOST_WINDOW]): the agent's reconnect backoff (1 s
 * doubling to 60 s, full jitter, transport.go) may take tens of seconds after the server is back,
 * and a step must not be lost for that. The short window is for the cases
 * where the agent does not come back (7–9).
 * Each test enrolls its own agent; the server is shared, restarted by two of them.
 */
@Timeout(value = 5, unit = TimeUnit.MINUTES)
class StreamBreakTest {
    /** Case 1: a network cut longer than the server's offline threshold, shorter than the lost window. */
    @Test
    fun `a network cut shorter than the lost window leaves one succeeded step`() {
        val stand = agent("cut")
        val started = backup(stand, STEP_SECONDS)

        val cutAt = databaseNow()
        Interruptions.cut(sard, stand.container)
        // The server closes the silent session after OFFLINE and starts the step's lost window.
        val deadline = Await.value("the lost deadline the server sets when the session ends", OFFLINE.multipliedBy(3)) { lostDeadline(started.stepId) }
        val cutFor = Duration.between(cutAt, databaseNow())
        Interruptions.reconnect(sard, stand.container, stand.agent.host.hostname)

        assertTrue(cutFor < LOST_WINDOW, "the cut lasted $cutFor, not shorter than the lost window $LOST_WINDOW")
        val window = Duration.between(cutAt, deadline)
        assertTrue(window >= LOST_WINDOW && window <= LOST_WINDOW + OFFLINE.multipliedBy(2), "lost deadline $window after the cut, want $LOST_WINDOW + up to the offline threshold")
        val step = Backups.awaitFinished(sard, started.stepId, STEP_TIMEOUT)
        assertEquals("succeeded", step.status, step.message)
        assertTrue(finishedAt(started.stepId) > cutAt, "the step finished before the cut")
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
     * Case 6: the backup ends and its result reaches the server, but the ResultAck does not reach
     * the agent (iptables drops what the server sends). The agent drops the dead stream, sends the
     * result again on the next one, the server records nothing new and acknowledges, and the agent
     * keeps the tombstone: it never sends that result again.
     */
    @Test
    fun `a result whose ack is lost is sent again, recorded once, and then no more`() {
        val stand = agent("ack")
        val started = backup(stand, SHORT_STEP_SECONDS)
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
        assertTrue(stand.container.holds("acked", started.stepId), "no tombstone of ${started.stepId}: the agent would send it again")
        assertFalse(stand.container.holds("results", started.stepId), "the agent still holds the result of ${started.stepId} to send")
        assertEquals("succeeded", RunRows.statusOf(sard, started.stepId))
        stand.once.assertOne(started)
    }

    /** Cases 2 and 3: [away] takes the server down while the step runs and brings it back. */
    private fun serverGoesAway(
        name: String,
        away: () -> Unit,
    ) {
        val stand = agent(name)
        val started = backup(stand, STEP_SECONDS)

        val awayAt = databaseNow()
        away()
        Await.until("Register of the agent after the server came back", RECONNECT_TIMEOUT) { registeredAfter(stand.agent.agentId, awayAt) }
        val step = Backups.awaitFinished(sard, started.stepId, STEP_TIMEOUT)

        assertEquals("succeeded", step.status, step.message)
        assertEquals(0, ExactlyOnce.count(sard.serverLogs(), "step ${started.stepId} sent again"), "the server sent the step again")
        assertSentOnce(stand, started)
        stand.once.assertOne(started)
    }

    /** The agent took the step once and never got it again (A4 dedup would log a repeated command). */
    private fun assertSentOnce(
        stand: Stand,
        started: Backups.Started,
    ) {
        assertEquals(1, stand.once.agentLines("step accepted", started.stepId), "the agent accepted the step more than once")
        assertEquals(0, stand.once.agentLines("repeated command", started.stepId), "the server sent the step again")
    }

    private class Stand(
        val agent: EnrolledAgent,
        val container: GenericContainer<*>,
        val once: ExactlyOnce,
    )

    /** An enrolled agent with a local repository, running and registered. */
    private fun agent(hostname: String): Stand {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = hostname, local = LOCAL)
        val init = agent.host.repoInit(REPOSITORY, "--generate-password")
        assertEquals(0, init.code, init.stderr)
        val container = sard.track("agent-$hostname", AgentContainer.of(agent)).apply { start() }
        Await.until("repository_id of $REPOSITORY in Register") { Backups.repositoryId(sard, agent.agentId, REPOSITORY) != null }
        return Stand(agent, container, ExactlyOnce(sard, container, agent.host, REPOSITORY_URL, PASSWORD_FILE))
    }

    /** Starts an `e2e-slow` backup of about [seconds] and returns once the step runs. */
    private fun backup(
        stand: Stand,
        seconds: Int,
    ): Backups.Started {
        val config = """{"size":${seconds * RATE},"rate":$RATE,"chunk":$CHUNK,"seed":$SEED}"""
        val started = Backups.start(sard, stand.agent.agentId, stand.agent.host.hostname, config, plugin = "e2e-slow")
        Await.until("step ${started.stepId} running") { RunRows.statusOf(sard, started.stepId) == "running" }
        return started
    }

    /** Whether the agent's executor holds [stepId]'s file in [dir] (store.go; docker cp, the image has no shell). */
    private fun GenericContainer<*>.holds(
        dir: String,
        stepId: UUID,
    ): Boolean {
        val digest = MessageDigest.getInstance("SHA-256").digest(stepId.toString().toByteArray())
        val path = "${AgentHost.EXECUTOR_DIR}/$dir/${HexFormat.of().formatHex(digest)}.json"
        return runCatching { copyFileFromContainer(path) { it.readAllBytes() } }.isSuccess
    }

    private fun lostDeadline(stepId: UUID): Instant? =
        one("SELECT lost_deadline FROM run_steps WHERE id = ?", stepId) { it.getObject(1, OffsetDateTime::class.java)?.toInstant() }

    private fun finishedAt(stepId: UUID): Instant =
        checkNotNull(one("SELECT finished_at FROM run_steps WHERE id = ?", stepId) { it.getObject(1, OffsetDateTime::class.java)?.toInstant() })

    private fun registeredAfter(
        agentId: String,
        instant: Instant,
    ): Boolean =
        sard.database().use { connection ->
            connection.prepareStatement("SELECT 1 FROM agents WHERE id = ? AND last_register_at > ?").use { q ->
                q.setObject(1, UUID.fromString(agentId))
                q.setTimestamp(2, Timestamp.from(instant))
                q.executeQuery().use { it.next() }
            }
        }

    private fun databaseNow(): Instant =
        sard.database().use { connection ->
            connection.createStatement().use { statement ->
                statement.executeQuery("SELECT now()").use {
                    it.next()
                    it.getObject(1, OffsetDateTime::class.java).toInstant()
                }
            }
        }

    private fun <T> one(
        sql: String,
        id: UUID,
        read: (java.sql.ResultSet) -> T?,
    ): T? =
        sard.database().use { connection ->
            connection.prepareStatement(sql).use { q ->
                q.setObject(1, id)
                q.executeQuery().use { if (it.next()) read(it) else null }
            }
        }

    companion object {
        private val HEARTBEAT: Duration = Duration.ofSeconds(5)
        private const val LOST_AFTER_HEARTBEATS = 20L

        /** The server closes a session silent this long (3 heartbeats, application.yaml). */
        private val OFFLINE: Duration = HEARTBEAT.multipliedBy(3)

        /** A step in flight without its agent is lost after this (S6a, FXs). */
        private val LOST_WINDOW: Duration = HEARTBEAT.multipliedBy(LOST_AFTER_HEARTBEATS)

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

        /** Long enough for a cut or a server restart to land mid-step. */
        private const val STEP_SECONDS = 30

        /**
         * Case 6: the result must be sent before the blocked stream dies. gRPC drops a connection
         * whose data stays unacknowledged for the keepalive timeout (10 s, transport.go); the step
         * ends a few seconds after the block.
         */
        private const val SHORT_STEP_SECONDS = 4

        private val STEP_TIMEOUT: Duration = Duration.ofMinutes(2)
        private val RECONNECT_TIMEOUT: Duration = Duration.ofMinutes(2)

        private const val RATE = 1 shl 20
        private const val CHUNK = 64 shl 10
        private const val SEED = 20261005L

        private const val REPOSITORY = "main"
        private const val REPOSITORY_URL = "${AgentHost.STATE_DIR}/repo"
        private const val PASSWORD_FILE = "${AgentHost.STATE_DIR}/$REPOSITORY.pass"

        private val LOCAL =
            """
            repositories:
              - name: $REPOSITORY
                url: $REPOSITORY_URL
                password_file: $PASSWORD_FILE
            """.trimIndent() + "\n"
    }
}
