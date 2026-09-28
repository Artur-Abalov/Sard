// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.protobuf.ByteString
import com.google.rpc.ErrorInfo
import dev.sard.proto.agent.v1.AgentServiceGrpc
import dev.sard.proto.agent.v1.EnrollRequest
import dev.sard.proto.agent.v1.EnrollmentServiceGrpc
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.FileCertificateAuthority
import dev.sard.server.pki.Pem
import io.grpc.CallOptions
import io.grpc.ChannelCredentials
import io.grpc.Grpc
import io.grpc.ManagedChannel
import io.grpc.Metadata
import io.grpc.MethodDescriptor
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.TlsChannelCredentials
import io.grpc.health.v1.HealthCheckRequest
import io.grpc.health.v1.HealthCheckResponse
import io.grpc.health.v1.HealthGrpc
import io.grpc.protobuf.StatusProto
import io.grpc.stub.ClientCalls
import io.grpc.stub.StreamObserver
import org.bouncycastle.asn1.x500.X500Name
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder
import org.bouncycastle.pkcs.jcajce.JcaPKCS10CertificationRequestBuilder
import org.junit.jupiter.api.io.TempDir
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.nio.file.Path
import java.security.KeyPair
import java.security.KeyPairGenerator
import java.security.SecureRandom
import java.security.spec.ECGenParameterSpec
import java.time.Clock
import java.time.Duration
import java.util.UUID
import java.util.concurrent.CompletableFuture
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val TTL: Duration = Duration.ofHours(1)
private const val DEADLINE_SECONDS = 10L

/** The outcome of one raw call: its status and, when refused, the ErrorInfo reason. */
private data class Outcome(
    val code: Status.Code,
    val reason: String? = null,
    val body: String? = null,
)

/** A key pair and the certificate chain the server issued for it. */
private class AgentKey(
    val identity: AgentIdentity,
    val serial: String,
    val chainPem: String,
    val keys: KeyPair,
)

/**
 * S3: the interceptor lets a call through only with the certificate of a live agent,
 * except for the services listed in [UNAUTHENTICATED_SERVICES] (ADR 0009).
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, ProbeServiceConfiguration::class)
class AgentAuthIntegrationTest(
    @Autowired private val ca: CertificateAuthority,
    @Autowired private val enrollment: Enrollment,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val jdbc: JdbcTemplate,
    @LocalGrpcServerPort private val grpcPort: Int,
) {
    @TempDir
    lateinit var tmp: Path

    private val acme = UUID.randomUUID()
    private val globex = UUID.randomUUID()
    private val channels = mutableListOf<ManagedChannel>()

    @BeforeTest
    fun `two fresh tenants`() {
        for (tenant in listOf(acme, globex)) {
            jdbc.update("insert into tenants (id, name) values (?, ?)", tenant, "t-$tenant")
        }
    }

    @AfterTest
    fun `drop both tenants`() {
        channels.forEach { it.shutdownNow() }
        for (tenant in listOf(acme, globex)) {
            for (table in listOf("agent_certificates", "enrollment_tokens", "agents")) {
                jdbc.update("delete from $table where tenant_id = ?", tenant)
            }
            jdbc.update("delete from tenants where id = ?", tenant)
        }
    }

    // --- fixtures

    private fun newKeys(): KeyPair =
        KeyPairGenerator.getInstance("EC").apply { initialize(ECGenParameterSpec("secp256r1")) }.generateKeyPair()

    private fun csr(keys: KeyPair): ByteArray {
        val signer = JcaContentSignerBuilder("SHA256withECDSA").build(keys.private)
        return JcaPKCS10CertificationRequestBuilder(X500Name("CN=test"), keys.public).build(signer).encoded
    }

    /** An agent enrolled into [tenant] the way a real one is: token, CSR, Enroll. */
    private fun enrolled(tenant: UUID): AgentKey {
        val keys = newKeys()
        val agent = enrollment.enroll(tokens.create(tenant, TTL).reveal(), csr(keys), "host-$tenant")
        val sql = "select serial from agent_certificates where agent_id = ?"
        val serial = jdbc.queryForObject(sql, String::class.java, agent.agentId)
        return AgentKey(AgentIdentity(tenant, agent.agentId), serial!!, agent.certificateChainPem, keys)
    }

    /** A certificate [issuer] signed for [identity] that the server has no record of. */
    private fun unrecorded(
        issuer: CertificateAuthority,
        identity: AgentIdentity,
    ): AgentKey {
        val keys = newKeys()
        val issued = issuer.issueAgentCertificate(csr(keys), identity)
        return AgentKey(identity, issued.serial.toString(16), issued.chainPem, keys)
    }

    private fun trusting() = TlsChannelCredentials.newBuilder().trustManager(ca.caBundlePem().byteInputStream())

    private fun anonymous(): ChannelCredentials = trusting().build()

    private fun presenting(agent: AgentKey): ChannelCredentials =
        trusting()
            .keyManager(agent.chainPem.byteInputStream(), Pem.privateKey(agent.keys.private).byteInputStream())
            .build()

    private fun channel(credentials: ChannelCredentials) =
        Grpc.newChannelBuilderForAddress("localhost", grpcPort, credentials).build().also { channels += it }

    /** One request of empty bytes to [fullMethodName]; streaming methods get one message and a half-close. */
    private fun call(
        credentials: ChannelCredentials,
        fullMethodName: String,
    ): Outcome =
        try {
            val options = CallOptions.DEFAULT.withDeadlineAfter(DEADLINE_SECONDS, TimeUnit.SECONDS)
            val method = rawMethod(fullMethodName)
            val body = ClientCalls.blockingUnaryCall(channel(credentials), method, options, ByteArray(0))
            Outcome(Status.Code.OK, body = String(body))
        } catch (e: StatusRuntimeException) {
            Outcome(e.status.code, reasonOf(e.status, e.trailers ?: Metadata()))
        }

    private fun reasonOf(
        status: Status,
        trailers: Metadata,
    ): String? {
        val details = StatusProto.fromStatusAndTrailers(status, trailers).detailsList
        return details
            .singleOrNull()
            ?.unpack(ErrorInfo::class.java)
            ?.also { assertEquals("sard.dev", it.domain) }
            ?.reason
    }

    private fun agentMethods() = AgentServiceGrpc.getServiceDescriptor().methods.map { it.fullMethodName }

    private fun refused(reason: AgentAuthFailure) = Outcome(Status.Code.UNAUTHENTICATED, reason.name)

    // --- 1, 2: deny by default

    @Test
    fun `every AgentService method, read from the descriptor, is refused without a certificate`() {
        assertTrue(agentMethods().isNotEmpty())
        for (method in agentMethods()) {
            assertEquals(refused(AgentAuthFailure.CERT_MISSING), call(anonymous(), method), method)
        }
    }

    @Test
    fun `a service nobody listed is refused without a certificate`() {
        assertFalse(ProbeService.SERVICE in UNAUTHENTICATED_SERVICES)
        assertEquals(refused(AgentAuthFailure.CERT_MISSING), call(anonymous(), ProbeService.WHOAMI))
    }

    // --- 3: the open services

    @Test
    fun `Enroll reaches its handler without a client certificate`() {
        val keys = newKeys()
        val request =
            EnrollRequest
                .newBuilder()
                .setEnrollmentToken(tokens.create(acme, TTL).reveal())
                .setCsrDer(ByteString.copyFrom(csr(keys)))
                .setHostname("db1")
                .build()
        val response = EnrollmentServiceGrpc.newBlockingStub(channel(anonymous())).enroll(request)
        val sql = "select tenant_id from agents where id = ?::uuid"
        val tenant = jdbc.queryForObject(sql, UUID::class.java, response.agentId)
        assertEquals(acme, tenant)
    }

    @Test
    fun `the health check answers without a client certificate`() {
        val health = HealthGrpc.newBlockingStub(channel(anonymous()))
        val response = health.check(HealthCheckRequest.getDefaultInstance())
        assertEquals(HealthCheckResponse.ServingStatus.SERVING, response.status)
    }

    @Test
    fun `reflection is not served, so it lists nothing to anyone`() {
        val reflection = "grpc.reflection.v1.ServerReflection/ServerReflectionInfo"
        assertEquals(Outcome(Status.Code.UNIMPLEMENTED), call(anonymous(), reflection))
    }

    // --- 4: a live agent passes

    /**
     * What a method answers once past the interceptor: Connect is the stream manager (S5a), and
     * the one empty message [call] sends is not a Hello; the others are stubs until S4a.
     */
    private fun pastInterceptor(method: String): Outcome =
        if (method == AgentServiceGrpc.getConnectMethod().fullMethodName) {
            Outcome(Status.Code.FAILED_PRECONDITION, "HELLO_REQUIRED")
        } else {
            Outcome(Status.Code.UNIMPLEMENTED)
        }

    @Test
    fun `a live agent's certificate passes the interceptor on every AgentService method`() {
        val agent = enrolled(acme)
        for (method in agentMethods()) {
            assertEquals(pastInterceptor(method), call(presenting(agent), method), method)
        }
    }

    @Test
    fun `the handler sees the principal of the certificate`() {
        val agent = enrolled(globex)
        val expected = "${agent.identity.tenantId}/${agent.identity.agentId}/${agent.serial}"
        assertEquals(Outcome(Status.Code.OK, body = expected), call(presenting(agent), ProbeService.WHOAMI))
    }

    // --- 5: refusals

    @Test
    fun `a certificate from another CA fails the handshake before any interceptor`() {
        val stranger =
            FileCertificateAuthority(tmp.resolve("pki"), listOf("localhost"), Clock.systemUTC(), SecureRandom())
        val agent = unrecorded(stranger, AgentIdentity(acme, UUID.randomUUID()))
        assertEquals(Outcome(Status.Code.UNAVAILABLE), call(presenting(agent), ProbeService.WHOAMI))
    }

    @Test
    fun `a certificate of our CA with no record is CERT_UNKNOWN`() {
        val agent = unrecorded(ca, AgentIdentity(acme, UUID.randomUUID()))
        assertEquals(refused(AgentAuthFailure.CERT_UNKNOWN), call(presenting(agent), ProbeService.WHOAMI))
    }

    @Test
    fun `a revoked certificate is CERT_REVOKED`() {
        val agent = enrolled(acme)
        jdbc.update("update agent_certificates set revoked_at = now() where serial = ?", agent.serial)
        assertEquals(refused(AgentAuthFailure.CERT_REVOKED), call(presenting(agent), ProbeService.WHOAMI))
    }

    @Test
    fun `a certificate past its recorded not_after is CERT_EXPIRED`() {
        val agent = enrolled(acme)
        val sql =
            "update agent_certificates set issued_at = now() - interval '2 days', " +
                "not_after = now() - interval '1 day' where serial = ?"
        jdbc.update(sql, agent.serial)
        assertEquals(refused(AgentAuthFailure.CERT_EXPIRED), call(presenting(agent), ProbeService.WHOAMI))
    }

    @Test
    fun `a revoked agent is AGENT_REVOKED`() {
        val agent = enrolled(acme)
        jdbc.update("update agents set revoked_at = now() where id = ?", agent.identity.agentId)
        assertEquals(refused(AgentAuthFailure.AGENT_REVOKED), call(presenting(agent), ProbeService.WHOAMI))
    }

    @Test
    fun `a certificate whose SAN names another agent than its record is CERT_IDENTITY_MISMATCH`() {
        val owner = enrolled(acme)
        val impostor = unrecorded(ca, AgentIdentity(acme, UUID.randomUUID()))
        val sql =
            "insert into agent_certificates (serial, tenant_id, agent_id, issued_at, not_after) " +
                "values (?, ?, ?, now(), now() + interval '1 day')"
        jdbc.update(sql, impostor.serial, acme, owner.identity.agentId)
        assertEquals(refused(AgentAuthFailure.CERT_IDENTITY_MISMATCH), call(presenting(impostor), ProbeService.WHOAMI))
    }

    // --- 6: the agent's tenant in handlers and stream messages

    @Test
    fun `a coroutine handler on another dispatcher sees only the agent's tenant`() {
        val acmeAgent = enrolled(acme)
        val globexAgent = enrolled(globex)
        val asAcme = call(presenting(acmeAgent), ProbeService.AGENTS)
        assertEquals(Outcome(Status.Code.OK, body = acmeAgent.identity.agentId.toString()), asAcme)
        val asGlobex = call(presenting(globexAgent), ProbeService.AGENTS)
        assertEquals(Outcome(Status.Code.OK, body = globexAgent.identity.agentId.toString()), asGlobex)
    }

    @Test
    fun `messages arriving on an open stream are handled in the tenant it was opened with`() {
        val acmeAgent = enrolled(acme)
        enrolled(globex)
        val responses = LinkedBlockingQueue<String>()
        val closed = CompletableFuture<Status>()
        val observer =
            object : StreamObserver<ByteArray> {
                override fun onNext(value: ByteArray) {
                    responses.add(String(value))
                }

                override fun onError(t: Throwable) {
                    closed.complete(Status.fromThrowable(t))
                }

                override fun onCompleted() {
                    closed.complete(Status.OK)
                }
            }
        val method = rawMethod(ProbeService.AGENTS_STREAM, MethodDescriptor.MethodType.BIDI_STREAMING)
        val stream = channel(presenting(acmeAgent)).newCall(method, CallOptions.DEFAULT)
        val requests = ClientCalls.asyncBidiStreamingCall(stream, observer)
        val expected = acmeAgent.identity.agentId.toString()
        repeat(3) {
            // Each message is sent only after the previous answer: long after the stream opened.
            requests.onNext(ByteArray(0))
            assertEquals(expected, responses.poll(DEADLINE_SECONDS, TimeUnit.SECONDS))
        }
        requests.onCompleted()
        assertEquals(Status.Code.OK, closed.get(DEADLINE_SECONDS, TimeUnit.SECONDS).code)
    }

    @Test
    fun `a stream is refused when it opens without a certificate`() {
        val method = rawMethod(ProbeService.AGENTS_STREAM, MethodDescriptor.MethodType.BIDI_STREAMING)
        assertEquals(refused(AgentAuthFailure.CERT_MISSING), call(anonymous(), method.fullMethodName))
    }
}
