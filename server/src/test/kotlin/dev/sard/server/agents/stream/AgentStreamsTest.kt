// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import com.google.protobuf.Timestamp
import com.google.rpc.ErrorInfo
import dev.sard.proto.agent.v1.ConnectRequest
import dev.sard.proto.agent.v1.ConnectResponse
import dev.sard.proto.agent.v1.Heartbeat
import dev.sard.proto.agent.v1.Hello
import dev.sard.proto.agent.v1.LogChunk
import dev.sard.proto.agent.v1.LogLine
import dev.sard.proto.agent.v1.RunStep
import dev.sard.proto.agent.v1.StepProgress
import dev.sard.proto.agent.v1.StepResult
import dev.sard.server.agents.AgentAuthFailure
import dev.sard.server.agents.AgentPrincipal
import dev.sard.server.agents.BatchStandings
import dev.sard.server.agents.CertificateStanding
import dev.sard.server.agents.stream.StreamFixtures.HEARTBEAT
import dev.sard.server.agents.stream.StreamFixtures.NOW
import dev.sard.server.agents.stream.StreamFixtures.SETTINGS
import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.MovableClock
import io.grpc.Context
import io.grpc.Metadata
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.kotlin.GrpcContextElement
import io.grpc.protobuf.StatusProto
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.consumeAsFlow
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.supervisorScope
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.yield
import org.junit.jupiter.api.extension.ExtendWith
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import java.time.Duration
import java.time.Instant
import java.util.Collections
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.test.fail

private const val WAIT_MS = 5_000L

/** Every extension point, recording what it was called with, in order. */
private class Recorder :
    CommandReconciliation,
    StepProgressHandler,
    StepResultHandler,
    LogChunkHandler,
    AgentSessionListener,
    LastSeenStore,
    StreamMetrics {
    val events: MutableList<String> = Collections.synchronizedList(mutableListOf())
    val signals = Channel<String>(Channel.UNLIMITED)

    private fun record(event: String) {
        events += event
        signals.trySend(event)
    }

    override fun onHello(
        agent: ConnectedAgent,
        runningCommandIds: List<String>,
    ) = record("hello ${runningCommandIds.joinToString(",")}")

    override fun handle(
        agent: ConnectedAgent,
        progress: StepProgress,
    ) = record("progress ${progress.commandId}")

    override fun handle(
        agent: ConnectedAgent,
        result: StepResult,
    ) = record("result ${result.commandId}")

    override fun handle(
        agent: ConnectedAgent,
        chunk: LogChunk,
    ) = record("log ${chunk.commandId}")

    override fun connected(agent: ConnectedAgent) = record("connected")

    override fun disconnected(
        agent: ConnectedAgent,
        reason: String,
    ) = record("disconnected $reason")

    override fun duplicateDetected(agent: ConnectedAgent) = record("duplicate")

    override fun duplicateDetected() = record("metric duplicate")

    override fun clockSkew(skew: Duration) = record("metric skew $skew")

    override fun record(
        agent: ConnectedAgent,
        at: Instant,
    ) = record("last-seen $at")

    suspend fun await(event: String) {
        withTimeout(WAIT_MS) {
            while (signals.receive() != event) {
                continue
            }
        }
    }
}

/** Certificate records by serial; the test's agent starts live. */
private class FakeStandings : BatchStandings {
    val records: MutableMap<String, CertificateStanding> = mutableMapOf()

    override fun of(serials: Collection<String>): Map<String, CertificateStanding> = records.filterKeys { it in serials }
}

private fun runStep(command: String) = ConnectResponse.newBuilder().setRunStep(RunStep.newBuilder().setCommandId(command)).build()

private fun hello(vararg running: String) =
    ConnectRequest.newBuilder().setHello(Hello.newBuilder().addAllRunningCommandIds(running.toList())).build()

private fun heartbeat(sentAt: Instant = NOW) =
    ConnectRequest
        .newBuilder()
        .setHeartbeat(Heartbeat.newBuilder().setSentAt(Timestamp.newBuilder().setSeconds(sentAt.epochSecond)))
        .build()

private fun progress(command: String): ConnectRequest {
    val progress = StepProgress.newBuilder().setCommandId(command)
    return ConnectRequest.newBuilder().setStepProgress(progress).build()
}

private fun result(command: String): ConnectRequest {
    val result = StepResult.newBuilder().setCommandId(command)
    return ConnectRequest.newBuilder().setStepResult(result).build()
}

private fun log(command: String) =
    ConnectRequest
        .newBuilder()
        .setLogChunk(LogChunk.newBuilder().setCommandId(command).addLines(LogLine.newBuilder().setText("x")))
        .build()

/** One agent-side stream: what the agent sends, and how the server's response flow ended. */
private class AgentSide(
    val requests: Channel<ConnectRequest>,
    val responses: Deferred<List<ConnectResponse>>,
) {
    suspend fun closedWith(): Pair<Status.Code, String?> =
        try {
            withTimeout(WAIT_MS) { responses.await() }
            Status.Code.OK to null
        } catch (e: StatusRuntimeException) {
            val details = StatusProto.fromStatusAndTrailers(e.status, e.trailers ?: Metadata()).detailsList
            e.status.code to details.single().unpack(ErrorInfo::class.java).reason
        }
}

@ExtendWith(OutputCaptureExtension::class)
class AgentStreamsTest {
    private val clock = MovableClock(NOW)
    private val recorder = Recorder()
    private val registry = AgentSessionRegistry(clock, SETTINGS)
    private val standings = FakeStandings()
    private val principal = AgentPrincipal(UUID.randomUUID(), UUID.randomUUID(), "8f0e5c2a9b7d4e6f8a1b2c3d4e5f6a7b")
    private val streams =
        AgentStreams(
            registry,
            SETTINGS,
            clock,
            Dispatchers.Unconfined,
            StreamExtensions(recorder, recorder, recorder, recorder, listOf(recorder), recorder, recorder),
            SessionRevalidation(standings, clock),
        )

    init {
        val identity = AgentIdentity(tenantId = principal.tenantId, agentId = principal.agentId)
        standings.records[principal.serial] = CertificateStanding(identity, NOW + Duration.ofDays(1), null, null)
    }

    private fun CoroutineScope.open(): AgentSide {
        val requests = Channel<ConnectRequest>(Channel.UNLIMITED)
        val context = GrpcContextElement(Context.current().withValue(AgentPrincipal.KEY, principal))
        val responses = async(context) { streams.connect(requests.consumeAsFlow()).toList() }
        return AgentSide(requests, responses)
    }

    /** A failed stream must not cancel the test: the refusal is what the test awaits. */
    private fun test(block: suspend CoroutineScope.() -> Unit) = runBlocking { supervisorScope { block() } }

    @Test
    fun `a first message other than Hello closes the stream with HELLO_REQUIRED`() =
        test {
            val agent = open()
            agent.requests.send(heartbeat())
            assertEquals(Status.Code.FAILED_PRECONDITION to "HELLO_REQUIRED", agent.closedWith())
            assertEquals(emptyList(), recorder.events, "no session, no event")
        }

    @Test
    fun `a stream that sends nothing is closed with HELLO_REQUIRED once the hello timeout passes`() =
        test {
            val agent = open()
            yield() // the stream starts and registers; the agent sends nothing
            clock.now = NOW + SETTINGS.helloTimeout - Duration.ofMillis(1)
            assertEquals(emptyList(), registry.sweep())
            clock.now = NOW + SETTINGS.helloTimeout
            assertEquals(1, registry.sweep().size)
            assertEquals(Status.Code.FAILED_PRECONDITION to "HELLO_REQUIRED", agent.closedWith())
            assertEquals(emptyList(), recorder.events)
        }

    @Test
    fun `Hello takes the slot, records last_seen_at and hands running command ids to reconciliation`() =
        test {
            val agent = open()
            agent.requests.send(hello("c-1", "c-2"))
            recorder.await("hello c-1,c-2")
            assertEquals(listOf("connected", "last-seen $NOW", "hello c-1,c-2"), recorder.events)
            assertTrue(registry.online(principal.agentId))
            agent.requests.close()
            assertEquals(Status.Code.OK to null, agent.closedWith())
            assertEquals("disconnected STREAM_ENDED", recorder.events.last())
        }

    @Test
    fun `progress, results and logs go to their handlers in order`() =
        test {
            val agent = open()
            agent.requests.send(hello())
            agent.requests.send(progress("c-1"))
            agent.requests.send(log("c-1"))
            agent.requests.send(result("c-1"))
            recorder.await("result c-1")
            assertEquals(listOf("progress c-1", "log c-1", "result c-1"), recorder.events.takeLast(3))
            agent.requests.close()
            agent.closedWith()
        }

    @Test
    fun `a repeated Hello is ignored`() =
        test {
            val agent = open()
            agent.requests.send(hello("c-1"))
            agent.requests.send(hello("c-2"))
            agent.requests.send(progress("sync"))
            recorder.await("progress sync")
            assertEquals(1, recorder.events.count { it.startsWith("hello") })
            agent.requests.close()
            agent.closedWith()
        }

    @Test
    fun `last_seen_at is written at most once per heartbeat interval`() =
        test {
            val agent = open()
            agent.requests.send(hello())
            recorder.await("hello ")
            clock.now = NOW + HEARTBEAT - Duration.ofSeconds(1)
            agent.requests.send(heartbeat(clock.now))
            agent.requests.send(progress("a"))
            recorder.await("progress a")
            clock.now = NOW + HEARTBEAT
            agent.requests.send(heartbeat(clock.now))
            agent.requests.send(progress("b"))
            recorder.await("progress b")
            val written = recorder.events.filter { it.startsWith("last-seen") }
            assertEquals(listOf("last-seen $NOW", "last-seen ${NOW + HEARTBEAT}"), written)
            agent.requests.close()
            agent.closedWith()
        }

    @Test
    fun `a second live stream is refused and the first one proving alive reports a duplicate`() =
        test {
            val first = open()
            first.requests.send(hello())
            recorder.await("hello ")
            val second = open()
            second.requests.send(hello())
            assertEquals(Status.Code.ALREADY_EXISTS to "AGENT_DUPLICATE_SESSION", second.closedWith())
            assertEquals(0, recorder.events.count { it == "duplicate" }, "not before the first session proves alive")

            first.requests.send(progress("alive"))
            recorder.await("progress alive")
            assertEquals(listOf("duplicate", "progress alive"), recorder.events.takeLast(2))
            first.requests.close()
            first.closedWith()
        }

    @Test
    fun `a second stream after the first went silent replaces it`() =
        test {
            val first = open()
            first.requests.send(hello())
            recorder.await("hello ")
            clock.now = NOW + SETTINGS.duplicateWindow
            val second = open()
            second.requests.send(hello())
            assertEquals(Status.Code.UNAVAILABLE to "SESSION_REPLACED", first.closedWith())
            recorder.await("hello ")
            assertTrue(registry.online(principal.agentId))
            assertEquals(0, recorder.events.count { it.startsWith("disconnected") }, "the slot stayed taken")
            second.requests.close()
            second.closedWith()
        }

    @Test
    fun `sweep closes a silent session with SESSION_EXPIRED and the agent goes offline`() =
        test {
            val agent = open()
            agent.requests.send(hello())
            recorder.await("hello ")
            clock.now = NOW + SETTINGS.offlineAfter
            registry.sweep()
            assertEquals(Status.Code.UNAVAILABLE to "SESSION_EXPIRED", agent.closedWith())
            assertEquals("disconnected SESSION_EXPIRED", recorder.events.last())
            assertEquals(false, registry.online(principal.agentId))
        }

    @Test
    fun `a heartbeat whose clock is off by more than the threshold is logged once per stream`(output: CapturedOutput) =
        test {
            val agent = open()
            agent.requests.send(hello())
            val ahead = NOW + SETTINGS.clockSkewThreshold + Duration.ofSeconds(1)
            repeat(2) { agent.requests.send(heartbeat(ahead)) }
            agent.requests.send(heartbeat(NOW + SETTINGS.clockSkewThreshold))
            agent.requests.send(progress("sync"))
            recorder.await("progress sync")
            val lines = output.out.lines().filter { "clock skew" in it }
            assertEquals(1, lines.size, lines.joinToString("\n"))
            assertTrue("PT11S" in lines.single(), lines.single())
            agent.requests.close()
            agent.closedWith()
        }

    @Test
    fun `a stream without an authenticated principal fails loudly`() =
        test {
            val requests = Channel<ConnectRequest>(Channel.UNLIMITED)
            try {
                streams.connect(requests.consumeAsFlow()).toList()
                fail("expected IllegalStateException")
            } catch (expected: IllegalStateException) {
                assertTrue("authenticated" in expected.message.orEmpty())
            }
        }

    // --- phase 3: send, database check, close, shutdown, metrics

    @Test
    fun `send queues messages the agent receives in order`() =
        test {
            val agent = open()
            agent.requests.send(hello())
            recorder.await("hello ")
            assertEquals(SendResult.Queued, streams.send(principal.agentId, runStep("c-1")))
            assertEquals(SendResult.Queued, streams.send(principal.agentId, runStep("c-2")))
            agent.requests.close()
            assertEquals(listOf(runStep("c-1"), runStep("c-2")), withTimeout(WAIT_MS) { agent.responses.await() })
        }

    @Test
    fun `send to an agent without a session is NotConnected, before Hello too`() =
        test {
            assertEquals(SendResult.NotConnected, streams.send(UUID.randomUUID(), runStep("c")))
            val agent = open()
            yield()
            assertEquals(SendResult.NotConnected, streams.send(principal.agentId, runStep("c")))
            agent.requests.close()
            agent.closedWith()
        }

    @Test
    fun `the database check closes a session whose certificate was revoked, before the expiry sweep`() =
        test {
            val agent = open()
            agent.requests.send(hello())
            recorder.await("hello ")
            standings.records.computeIfPresent(principal.serial) { _, standing -> standing.copy(revokedAt = NOW) }
            clock.now = NOW + SETTINGS.offlineAfter
            streams.check()
            assertEquals(Status.Code.UNAUTHENTICATED to "CERT_REVOKED", agent.closedWith())
            assertEquals("disconnected CERT_REVOKED", recorder.events.last())
        }

    @Test
    fun `the database check leaves a valid session open and still sweeps`() =
        test {
            val agent = open()
            agent.requests.send(hello())
            recorder.await("hello ")
            streams.check()
            assertTrue(registry.online(principal.agentId))
            clock.now = NOW + SETTINGS.offlineAfter
            streams.check()
            assertEquals(Status.Code.UNAVAILABLE to "SESSION_EXPIRED", agent.closedWith())
        }

    @Test
    fun `close by agent id ends its session with the auth failure`() =
        test {
            assertFalse(streams.close(principal.agentId, AgentAuthFailure.AGENT_REVOKED))
            val agent = open()
            agent.requests.send(hello())
            recorder.await("hello ")
            assertTrue(streams.close(principal.agentId, AgentAuthFailure.AGENT_REVOKED))
            assertEquals(Status.Code.UNAUTHENTICATED to "AGENT_REVOKED", agent.closedWith())
        }

    @Test
    fun `shutdown closes every stream with SERVER_SHUTTING_DOWN and refuses new ones`() =
        test {
            val agent = open()
            agent.requests.send(hello())
            recorder.await("hello ")
            streams.shutdown()
            assertEquals(Status.Code.UNAVAILABLE to "SERVER_SHUTTING_DOWN", agent.closedWith())
            val late = open()
            assertEquals(Status.Code.UNAVAILABLE to "SERVER_SHUTTING_DOWN", late.closedWith())
        }

    @Test
    fun `duplicates and clock skew reach the metrics`() =
        test {
            val first = open()
            first.requests.send(hello())
            recorder.await("hello ")
            val second = open()
            second.requests.send(hello())
            second.closedWith()
            first.requests.send(heartbeat(NOW - Duration.ofSeconds(5)))
            first.requests.send(progress("sync"))
            recorder.await("progress sync")
            assertTrue("metric duplicate" in recorder.events, recorder.events.toString())
            assertTrue("metric skew PT-5S" in recorder.events, recorder.events.toString())
            first.requests.close()
            first.closedWith()
        }
}
