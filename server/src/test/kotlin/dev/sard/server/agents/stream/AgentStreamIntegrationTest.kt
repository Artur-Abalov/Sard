// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import dev.sard.proto.agent.v1.ConnectResponse
import dev.sard.proto.agent.v1.RunStep
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import io.grpc.Status
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.context.properties.bind.Binder
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.core.env.Environment
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Duration
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** S5a tests 2-4 over the real server: TLS on a random port, PostgreSQL, a clock the test moves. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    // The periodic sweep stays out of the way; the tests sweep by hand after moving the clock.
    properties = ["spring.grpc.server.port=0", "sard.agent.stream.check-interval=1h"],
)
@Import(TestcontainersConfiguration::class, StreamTestConfiguration::class)
class AgentStreamIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @LocalGrpcServerPort port: Int,
    @Autowired private val clock: MovableClock,
    @Autowired private val recorded: RecordingExtensions,
    @Autowired private val registry: AgentSessionRegistry,
    @Autowired private val settings: AgentStreamSettings,
    @Autowired private val environment: Environment,
    @Autowired private val streams: AgentStreams,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val clients = StreamClients(ca, enrollment, tokens, jdbc, port)

    @BeforeTest
    fun `a fresh tenant`() = clients.createTenant()

    @AfterTest
    fun `drop the tenant`() = clients.close()

    /** Hello, and wait until the server took the slot and reconciled. */
    private fun greeted(agent: TestAgent): Connection =
        clients.connect(agent).also {
            it.hello()
            recorded.await("${agent.agentId} hello ")
        }

    // --- 1: keepalive settings (the behaviour itself: KeepaliveIntegrationTest)

    @Test
    fun `the server permits the agent's 30 s pings, without calls too, and pings back`() {
        val binder = Binder.get(environment)

        fun duration(key: String) = binder.bind("spring.grpc.server.keepalive.$key", Duration::class.java).get()
        assertTrue(duration("permit.time") < Duration.ofSeconds(30), "the agent pings every 30 s (A3)")
        assertEquals(true, binder.bind("spring.grpc.server.keepalive.permit.without-calls", Boolean::class.java).get())
        assertEquals(Duration.ofSeconds(30), duration("time"))
        assertEquals(Duration.ofSeconds(10), duration("timeout"))
    }

    @Test
    fun `the heartbeat interval is the one Register announces, 30 s by default`() {
        assertEquals(Duration.ofSeconds(30), settings.heartbeatInterval)
        assertEquals(Duration.ofSeconds(60), settings.duplicateWindow)
        assertEquals(Duration.ofSeconds(90), settings.offlineAfter)
    }

    // --- 2: Hello first

    @Test
    fun `a first message other than Hello closes the stream with HELLO_REQUIRED`() {
        val agent = clients.enrolled()
        val connection = clients.connect(agent)
        connection.progress("too-early")
        assertEquals(Ended(Status.Code.FAILED_PRECONDITION, "HELLO_REQUIRED"), connection.ended())
        assertFalse(registry.online(agent.agentId))
    }

    @Test
    fun `Hello hands running command ids to reconciliation and makes the agent online`() {
        val agent = clients.enrolled()
        val connection = clients.connect(agent)
        connection.hello("cmd-1", "cmd-2")
        val before = recorded.await("${agent.agentId} hello cmd-1,cmd-2")
        assertEquals("${agent.agentId} connected", before.last())
        assertTrue(registry.online(agent.agentId))
        assertTrue(connection.isOpen)
    }

    // --- 3: one session per agent

    @Test
    fun `a second stream while the first is alive is refused, and the duplicate is recorded once the first talks`() {
        val agent = clients.enrolled()
        val first = greeted(agent)
        val clone = clients.connect(agent)
        clone.hello()
        assertEquals(Ended(Status.Code.ALREADY_EXISTS, "AGENT_DUPLICATE_SESSION"), clone.ended())

        first.progress("alive")
        val before = recorded.await("${agent.agentId} progress alive")
        assertEquals(listOf("${agent.agentId} duplicate"), before)
        assertTrue(first.isOpen)
        assertTrue(registry.online(agent.agentId))
    }

    @Test
    fun `a second stream after the first went silent for the duplicate window replaces it`() {
        val agent = clients.enrolled()
        val stale = greeted(agent)
        clock.now += settings.duplicateWindow
        val fresh = clients.connect(agent)
        fresh.hello()
        recorded.await("${agent.agentId} hello ")

        assertEquals(Ended(Status.Code.UNAVAILABLE, "SESSION_REPLACED"), stale.ended())
        assertTrue(fresh.isOpen)
        fresh.progress("sync")
        val before = recorded.await("${agent.agentId} progress sync")
        assertFalse(before.any { "duplicate" in it || "disconnected" in it }, before.toString())
    }

    // --- 4: heartbeat, last_seen_at, expiry

    @Test
    fun `last_seen_at is written on Hello and then at most once per heartbeat interval`() {
        val agent = clients.enrolled()
        val start = clock.now
        val connection = greeted(agent)
        assertEquals(start, clients.lastSeenAt(agent))

        clock.now = start + settings.heartbeatInterval - Duration.ofSeconds(1)
        connection.heartbeat(clock.now)
        connection.progress("a")
        recorded.await("${agent.agentId} progress a")
        assertEquals(start, clients.lastSeenAt(agent), "within the interval")

        clock.now = start + settings.heartbeatInterval
        connection.heartbeat(clock.now)
        connection.progress("b")
        recorded.await("${agent.agentId} progress b")
        assertEquals(start + settings.heartbeatInterval, clients.lastSeenAt(agent))
    }

    @Test
    fun `a session silent for offlineAfter is closed by the sweep and the agent is offline`() {
        val agent = clients.enrolled()
        val connection = greeted(agent)
        clock.now += settings.offlineAfter - Duration.ofSeconds(1)
        registry.sweep()
        assertTrue(registry.online(agent.agentId))
        assertTrue(connection.isOpen)

        clock.now += Duration.ofSeconds(1)
        registry.sweep()
        assertEquals(Ended(Status.Code.UNAVAILABLE, "SESSION_EXPIRED"), connection.ended())
        recorded.await("${agent.agentId} disconnected SESSION_EXPIRED")
        assertFalse(registry.online(agent.agentId))
    }

    // --- 5: revocation and expiry close open sessions, without a restart

    private fun revokedBy(
        update: String,
        agent: TestAgent,
    ) = jdbc.update(update, Timestamp.from(clock.now), agent.agentId)

    @Test
    fun `revoking the agent closes its open stream with AGENT_REVOKED`() {
        val agent = clients.enrolled()
        val connection = greeted(agent)
        revokedBy("update agents set revoked_at = ? where id = ?", agent)
        streams.check()
        assertEquals(Ended(Status.Code.UNAUTHENTICATED, "AGENT_REVOKED"), connection.ended())
        recorded.await("${agent.agentId} disconnected AGENT_REVOKED")
    }

    @Test
    fun `revoking the certificate closes its open stream with CERT_REVOKED`() {
        val agent = clients.enrolled()
        val connection = greeted(agent)
        revokedBy("update agent_certificates set revoked_at = ? where agent_id = ?", agent)
        streams.check()
        assertEquals(Ended(Status.Code.UNAUTHENTICATED, "CERT_REVOKED"), connection.ended())
    }

    @Test
    fun `reaching not_after closes the open stream with CERT_EXPIRED`() {
        val agent = clients.enrolled()
        val connection = greeted(agent)
        val notAfter = Timestamp.from(clock.now + Duration.ofSeconds(1))
        jdbc.update("update agent_certificates set not_after = ? where agent_id = ?", notAfter, agent.agentId)
        streams.check()
        assertTrue(connection.isOpen, "a second before not_after")
        clock.now += Duration.ofSeconds(1)
        streams.check()
        assertEquals(Ended(Status.Code.UNAUTHENTICATED, "CERT_EXPIRED"), connection.ended())
    }

    @Test
    fun `the check leaves the other agents' sessions open`() {
        val revoked = clients.enrolled()
        val bystander = clients.enrolled()
        val closed = greeted(revoked)
        val open = greeted(bystander)
        revokedBy("update agents set revoked_at = ? where id = ?", revoked)
        streams.check()
        assertEquals(Ended(Status.Code.UNAUTHENTICATED, "AGENT_REVOKED"), closed.ended())
        assertTrue(open.isOpen)
        assertTrue(registry.online(bystander.agentId))
    }

    // --- 6: send, backpressure

    private fun runStep(
        command: String,
        padding: Int = 0,
    ) = ConnectResponse
        .newBuilder()
        .setRunStep(RunStep.newBuilder().setCommandId(command).setConfigJson("x".repeat(padding)))
        .build()

    @Test
    fun `send reaches the agent in order`() {
        val agent = clients.enrolled()
        val connection = greeted(agent)
        assertEquals(SendResult.Queued, streams.send(agent.agentId, runStep("c-1")))
        assertEquals(SendResult.Queued, streams.send(agent.agentId, runStep("c-2")))
        val got =
            List(2) {
                connection.received
                    .poll(WAIT_SECONDS, TimeUnit.SECONDS)
                    ?.runStep
                    ?.commandId
            }
        assertEquals(listOf("c-1", "c-2"), got)
    }

    @Test
    fun `send to an agent that is not connected is NotConnected`() {
        assertEquals(SendResult.NotConnected, streams.send(clients.enrolled().agentId, runStep("c")))
    }

    @Test
    fun `an agent that does not read fills only its own queue, and the sender learns it`() {
        val slow = clients.enrolled()
        val fast = clients.enrolled()
        val stalled = clients.connect(slow, reading = false).also { it.hello() }
        recorded.await("${slow.agentId} hello ")
        val quick = greeted(fast)

        // The client's flow-control window (1 MiB) and gRPC's buffer fill first, then the queue.
        val results = generateSequence(0) { it + 1 }.take(MAX_SENDS).map { streams.send(slow.agentId, runStep("s-$it", PADDING)) }
        val queued = results.takeWhile { it == SendResult.Queued }.count()
        assertTrue(queued < MAX_SENDS, "the queue never filled")
        assertEquals(SendResult.QueueFull, streams.send(slow.agentId, runStep("more")))

        assertEquals(SendResult.Queued, streams.send(fast.agentId, runStep("f-1")))
        assertEquals(
            "f-1",
            quick.received
                .poll(WAIT_SECONDS, TimeUnit.SECONDS)
                ?.runStep
                ?.commandId,
            "not held up",
        )

        stalled.read(queued)
        val first = stalled.received.poll(WAIT_SECONDS, TimeUnit.SECONDS)
        assertEquals("s-0", first?.runStep?.commandId, "the slow agent still gets its messages, in order")
        assertTrue(stalled.isOpen)
    }
}

private const val PADDING = 64 * 1024
private const val MAX_SENDS = 1_000
