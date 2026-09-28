// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import com.google.protobuf.Timestamp
import com.google.rpc.ErrorInfo
import dev.sard.proto.agent.v1.AgentServiceGrpc
import dev.sard.proto.agent.v1.ConnectRequest
import dev.sard.proto.agent.v1.ConnectResponse
import dev.sard.proto.agent.v1.Heartbeat
import dev.sard.proto.agent.v1.Hello
import dev.sard.proto.agent.v1.StepProgress
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.Pem
import io.grpc.ChannelCredentials
import io.grpc.Grpc
import io.grpc.ManagedChannel
import io.grpc.ManagedChannelBuilder
import io.grpc.Metadata
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.TlsChannelCredentials
import io.grpc.protobuf.StatusProto
import io.grpc.stub.ClientCallStreamObserver
import io.grpc.stub.ClientResponseObserver
import io.grpc.stub.StreamObserver
import org.bouncycastle.asn1.x500.X500Name
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder
import org.bouncycastle.pkcs.jcajce.JcaPKCS10CertificationRequestBuilder
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.jdbc.core.JdbcTemplate
import java.security.KeyPair
import java.security.KeyPairGenerator
import java.security.spec.ECGenParameterSpec
import java.time.Duration
import java.time.Instant
import java.time.temporal.ChronoUnit
import java.util.UUID
import java.util.concurrent.CompletableFuture
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit

const val WAIT_SECONDS = 10L
private val TOKEN_TTL: Duration = Duration.ofHours(1)

/** Every extension point S6/S7 will fill, recording calls as strings, awaitable in order. */
class RecordingExtensions :
    CommandReconciliation,
    StepProgressHandler,
    AgentSessionListener {
    private val events = LinkedBlockingQueue<String>()

    override fun onHello(
        agent: ConnectedAgent,
        runningCommandIds: List<String>,
    ) {
        events += "${agent.agentId} hello ${runningCommandIds.joinToString(",")}"
    }

    override fun handle(
        agent: ConnectedAgent,
        progress: StepProgress,
    ) {
        events += "${agent.agentId} progress ${progress.commandId}"
    }

    override fun connected(agent: ConnectedAgent) {
        events += "${agent.agentId} connected"
    }

    override fun disconnected(
        agent: ConnectedAgent,
        reason: String,
    ) {
        events += "${agent.agentId} disconnected $reason"
    }

    override fun duplicateDetected(agent: ConnectedAgent) {
        events += "${agent.agentId} duplicate"
    }

    /** Waits for [event], returning the events that came before it (other agents' included). */
    fun await(event: String): List<String> {
        val before = mutableListOf<String>()
        while (true) {
            val next = events.poll(WAIT_SECONDS, TimeUnit.SECONDS)
            checkNotNull(next) { "no '$event' within ${WAIT_SECONDS}s; saw $before" }
            if (next == event) return before
            before += next
        }
    }
}

/** A clock the test moves, starting at a whole second so Postgres keeps it exactly. */
@TestConfiguration(proxyBeanMethods = false)
class StreamTestConfiguration {
    @Bean
    fun clock() = MovableClock(Instant.now().truncatedTo(ChronoUnit.SECONDS))

    @Bean
    fun recordingExtensions() = RecordingExtensions()
}

/** An enrolled agent: its id and the client credentials of its certificate. */
class TestAgent(
    val tenantId: UUID,
    val agentId: UUID,
    val credentials: ChannelCredentials,
)

/** Enrolls agents the way a real one does and opens raw Connect streams for them. */
class StreamClients(
    private val ca: CertificateAuthority,
    private val enrollment: Enrollment,
    private val tokens: EnrollmentTokens,
    private val jdbc: JdbcTemplate,
    private val port: Int,
) {
    private val channels = mutableListOf<ManagedChannel>()
    val tenant: UUID = UUID.randomUUID()

    fun createTenant() {
        jdbc.update("insert into tenants (id, name) values (?, ?)", tenant, "t-$tenant")
    }

    fun closeChannels() = channels.forEach { it.shutdownNow() }

    fun close() {
        closeChannels()
        for (table in listOf("agent_certificates", "enrollment_tokens", "agents")) {
            jdbc.update("delete from $table where tenant_id = ?", tenant)
        }
        jdbc.update("delete from tenants where id = ?", tenant)
    }

    fun enrolled(): TestAgent {
        val generator = KeyPairGenerator.getInstance("EC").apply { initialize(ECGenParameterSpec("secp256r1")) }
        val keys = generator.generateKeyPair()
        val agent = enrollment.enroll(tokens.create(tenant, TOKEN_TTL).reveal(), csr(keys), "host-$tenant")
        val credentials =
            TlsChannelCredentials
                .newBuilder()
                .trustManager(ca.caBundlePem().byteInputStream())
                .keyManager(agent.certificateChainPem.byteInputStream(), Pem.privateKey(keys.private).byteInputStream())
                .build()
        return TestAgent(tenant, agent.agentId, credentials)
    }

    /** A Connect stream; with [reading] false the client takes no message until [Connection.read]. */
    fun connect(
        agent: TestAgent,
        reading: Boolean = true,
        tune: (ManagedChannelBuilder<*>) -> Unit = {},
    ): Connection {
        val builder = Grpc.newChannelBuilderForAddress("localhost", port, agent.credentials)
        tune(builder)
        val channel = builder.build().also { channels += it }
        return Connection(AgentServiceGrpc.newStub(channel), reading)
    }

    fun lastSeenAt(agent: TestAgent): Instant? =
        jdbc.queryForObject("select last_seen_at from agents where id = ?", Instant::class.java, agent.agentId)

    private fun csr(keys: KeyPair): ByteArray {
        val signer = JcaContentSignerBuilder("SHA256withECDSA").build(keys.private)
        return JcaPKCS10CertificationRequestBuilder(X500Name("CN=test"), keys.public).build(signer).encoded
    }
}

/** How a stream ended, as the agent sees it: the code and the ErrorInfo reason. */
data class Ended(
    val code: Status.Code,
    val reason: String? = null,
)

/** One Connect stream from the agent side. */
class Connection(
    stub: AgentServiceGrpc.AgentServiceStub,
    reading: Boolean = true,
) {
    val received = LinkedBlockingQueue<ConnectResponse>()
    private val ended = CompletableFuture<Ended>()
    private val flow = CompletableFuture<ClientCallStreamObserver<ConnectRequest>>()
    private val requests: StreamObserver<ConnectRequest> =
        stub.connect(
            object : ClientResponseObserver<ConnectRequest, ConnectResponse> {
                override fun beforeStart(requestStream: ClientCallStreamObserver<ConnectRequest>) {
                    if (!reading) requestStream.disableAutoRequestWithInitial(0)
                    flow.complete(requestStream)
                }

                override fun onNext(value: ConnectResponse) {
                    received += value
                }

                override fun onError(t: Throwable) {
                    ended.complete(endedBy(t))
                }

                override fun onCompleted() {
                    ended.complete(Ended(Status.Code.OK))
                }
            },
        )

    /** Lets [count] more server messages in (only for a connection opened with `reading = false`). */
    fun read(count: Int) = flow.get().request(count)

    val isOpen: Boolean
        get() = !ended.isDone

    @Synchronized
    fun send(message: ConnectRequest) = requests.onNext(message)

    fun hello(vararg running: String) =
        send(ConnectRequest.newBuilder().setHello(Hello.newBuilder().addAllRunningCommandIds(running.toList())).build())

    fun heartbeat(sentAt: Instant) {
        val at = Timestamp.newBuilder().setSeconds(sentAt.epochSecond).setNanos(sentAt.nano)
        send(ConnectRequest.newBuilder().setHeartbeat(Heartbeat.newBuilder().setSentAt(at)).build())
    }

    fun progress(command: String) =
        send(ConnectRequest.newBuilder().setStepProgress(StepProgress.newBuilder().setCommandId(command)).build())

    fun ended(seconds: Long = WAIT_SECONDS): Ended = ended.get(seconds, TimeUnit.SECONDS)

    /** Waits up to [duration] for the stream to end; null when it stayed open. */
    fun endedWithin(duration: Duration): Ended? {
        val millis = duration.toMillis()
        return runCatching { ended.get(millis, TimeUnit.MILLISECONDS) }.getOrNull()
    }

    private fun endedBy(t: Throwable): Ended {
        val e = t as? StatusRuntimeException ?: return Ended(Status.Code.UNKNOWN, t.toString())
        val details = StatusProto.fromStatusAndTrailers(e.status, e.trailers ?: Metadata()).detailsList
        val reason = details.singleOrNull()?.unpack(ErrorInfo::class.java)?.reason
        return Ended(e.status.code, reason)
    }
}
