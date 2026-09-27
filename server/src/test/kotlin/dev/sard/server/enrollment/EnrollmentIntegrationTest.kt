// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.proto.agent.v1.EnrollRequest
import dev.sard.proto.agent.v1.EnrollmentServiceGrpcKt
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.EnrollmentRejectedException.Reason
import dev.sard.server.pki.CaFingerprint
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.FileCertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.PkiFixtures.certificate
import dev.sard.server.pki.PkiFixtures.certificates
import dev.sard.server.pki.PkiFixtures.resource
import io.grpc.Grpc
import io.grpc.Status
import io.grpc.StatusException
import io.grpc.TlsChannelCredentials
import kotlinx.coroutines.runBlocking
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.nio.file.Path
import java.security.SecureRandom
import java.sql.Timestamp
import java.time.Clock
import java.time.Duration
import java.util.UUID
import java.util.concurrent.Callable
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.Semaphore
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** The real file CA, with a switch that holds a signature open until the test lets it go. */
class GatedCertificateAuthority(
    private val delegate: CertificateAuthority,
) : CertificateAuthority by delegate {
    @Volatile
    var gate: CountDownLatch? = null
    val entered = Semaphore(0)

    override fun issueAgentCertificate(
        csrDer: ByteArray,
        agent: dev.sard.server.pki.AgentIdentity,
    ): dev.sard.server.pki.IssuedCertificate {
        entered.release()
        gate?.await(ENROLLMENT_RACE_WAIT.toSeconds(), TimeUnit.SECONDS)
        return delegate.issueAgentCertificate(csrDer, agent)
    }
}

@TestConfiguration(proxyBeanMethods = false)
class EnrollmentTestConfiguration {
    @Bean
    fun clock() = MovableClock(ENROLLMENT_NOW)

    @Bean
    fun certificateAuthority(
        @Value("\${sard.pki.dir}") dir: Path,
    ) = GatedCertificateAuthority(FileCertificateAuthority(dir, listOf("localhost"), Clock.systemUTC(), SecureRandom()))
}

/** S2a: Enroll is one transaction that either enrolls the agent or leaves the token as it was. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, EnrollmentTestConfiguration::class)
class EnrollmentIntegrationTest(
    @Autowired private val enrollment: Enrollment,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val ca: GatedCertificateAuthority,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @LocalGrpcServerPort private val grpcPort: Int,
) {
    private val acme = UUID.randomUUID()
    private val csr = resource("agent-p256.csr")

    @BeforeTest
    fun `start at a known instant with a fresh tenant`() {
        clock.now = ENROLLMENT_NOW
        ca.gate = null
        ca.entered.drainPermits()
        jdbc.insertTenant(acme)
    }

    @AfterTest
    fun `drop everything of the tenant`() {
        jdbc.deleteEnrollmentTenantData(acme)
    }

    private fun newToken() = tokens.create(acme, ENROLLMENT_TOKEN_TTL)

    private fun tokenRow(issued: IssuedEnrollmentToken) =
        jdbc.queryForMap("select used_at, agent_id from enrollment_tokens where id = ?", issued.id)

    private fun count(table: String): Int? {
        val sql = "select count(*) from $table where tenant_id = ?"
        return jdbc.queryForObject(sql, Int::class.java, acme)
    }

    private fun assertUntouched(issued: IssuedEnrollmentToken) {
        assertEquals(mapOf<String, Any?>("used_at" to null, "agent_id" to null), tokenRow(issued))
        assertEquals(listOf(0, 0), listOf(count("agents"), count("agent_certificates")))
    }

    @Test
    fun `an enrolled agent joins the token's tenant with its certificate recorded`() {
        val issued = newToken()
        clock.now = ENROLLMENT_NOW + Duration.ofMinutes(5)
        val enrolled = enrollment.enroll(issued.reveal(), csr, "db1")

        assertEquals(7, enrolled.agentId.version())
        val agent = jdbc.queryForMap("select * from agents where id = ?", enrolled.agentId)
        assertEquals(acme, agent["tenant_id"])
        assertEquals("db1", agent["hostname"])
        assertNull(agent["agent_version"], "Register reports the version")
        assertEquals(clock.now, (agent["registered_at"] as Timestamp).toInstant())

        val leaf = certificates(enrolled.certificateChainPem).single()
        val uri = "sard://tenants/$acme/agents/${enrolled.agentId}"
        assertEquals(listOf(listOf<Any>(6, uri)), leaf.subjectAlternativeNames.map { it.toList() })
        assertEquals(ca.caBundlePem(), enrolled.caBundlePem)
        leaf.verify(certificate(enrolled.caBundlePem).publicKey)

        val cert = jdbc.queryForMap("select * from agent_certificates where agent_id = ?", enrolled.agentId)
        assertEquals(leaf.serialNumber.toString(16), cert["serial"])
        assertEquals(acme, cert["tenant_id"])
        assertEquals(leaf.notBefore.toInstant(), (cert["issued_at"] as Timestamp).toInstant())
        assertEquals(leaf.notAfter.toInstant(), (cert["not_after"] as Timestamp).toInstant())
        assertNull(cert["revoked_at"])

        val token = tokenRow(issued)
        assertEquals(clock.now, (token["used_at"] as Timestamp).toInstant())
        assertEquals(enrolled.agentId, token["agent_id"])
    }

    @Test
    fun `a failed signature leaves the token unused and nothing behind`() {
        val issued = newToken()
        val badCsr = "not a CSR".toByteArray()
        assertEquals(Reason.CSR_INVALID, rejectionReason { enrollment.enroll(issued.reveal(), badCsr, "db1") })
        assertUntouched(issued)
        enrollment.enroll(issued.reveal(), csr, "db1")
        assertEquals(1, count("agents"))
    }

    @Test
    fun `a used token is rejected`() {
        val issued = newToken()
        enrollment.enroll(issued.reveal(), csr, "db1")
        assertEquals(Reason.TOKEN_USED, rejectionReason { enrollment.enroll(issued.reveal(), csr, "db2") })
        assertEquals(listOf(1, 1), listOf(count("agents"), count("agent_certificates")))
    }

    @Test
    fun `a token expires at its expiry instant`() {
        val early = newToken()
        val late = newToken()
        clock.now = ENROLLMENT_NOW + ENROLLMENT_TOKEN_TTL - Duration.ofMillis(1)
        enrollment.enroll(early.reveal(), csr, "db1")
        clock.now = ENROLLMENT_NOW + ENROLLMENT_TOKEN_TTL
        assertEquals(Reason.TOKEN_EXPIRED, rejectionReason { enrollment.enroll(late.reveal(), csr, "db2") })
        assertEquals(mapOf<String, Any?>("used_at" to null, "agent_id" to null), tokenRow(late))
    }

    @Test
    fun `tokens that are malformed, unknown or pinned to another CA are rejected`() {
        val issued = newToken()
        val secret = EnrollmentToken.parse(issued.reveal()).secret
        val stranger = EnrollmentToken(secret, CaFingerprint("0".repeat(64))).encode()
        val unknown = EnrollmentToken(EnrollmentSecret.random(SecureRandom()), ca.fingerprint()).encode()

        assertEquals(Reason.TOKEN_MALFORMED, rejectionReason { enrollment.enroll("sard_", csr, "db1") })
        assertEquals(Reason.TOKEN_FOREIGN_CA, rejectionReason { enrollment.enroll(stranger, csr, "db1") })
        assertEquals(Reason.TOKEN_UNKNOWN, rejectionReason { enrollment.enroll(unknown, csr, "db1") })
        assertUntouched(issued)
    }

    @Test
    fun `of two concurrent enrollments with one token exactly one succeeds`() {
        val token = newToken().reveal()
        ca.gate = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(2)
        try {
            val first = pool.submit(Callable { runCatching { enrollment.enroll(token, csr, "first") } })
            val entered = ca.entered.tryAcquire(ENROLLMENT_RACE_WAIT.toSeconds(), TimeUnit.SECONDS)
            assertTrue(entered, "the first never reached the CA")
            val second = pool.submit(Callable { runCatching { enrollment.enroll(token, csr, "second") } })
            jdbc.awaitEnrollmentRowLockWait()
            ca.gate?.countDown()
            val results = listOf(first.get(), second.get())

            assertTrue(results[0].isSuccess, "the first holds the token: ${results[0]}")
            val loser = results[1].exceptionOrNull()
            assertEquals(Reason.TOKEN_USED, (loser as EnrollmentRejectedException).reason)
            assertEquals(listOf(1, 1), listOf(count("agents"), count("agent_certificates")))
        } finally {
            ca.gate?.countDown()
            pool.shutdownNow()
        }
    }

    // --- over gRPC

    private fun grpcEnroll(request: EnrollRequest) =
        Grpc
            .newChannelBuilderForAddress(
                "localhost",
                grpcPort,
                TlsChannelCredentials.newBuilder().trustManager(ca.caBundlePem().byteInputStream()).build(),
            ).build()
            .let { channel ->
                try {
                    val stub = EnrollmentServiceGrpcKt.EnrollmentServiceCoroutineStub(channel)
                    runCatching { runBlocking { stub.enroll(request) } }
                } finally {
                    channel.shutdownNow()
                }
            }

    private fun code(result: Result<*>) = (result.exceptionOrNull() as StatusException).status.code

    @Test
    fun `Enroll over gRPC answers the agent id, its certificate and the CA bundle`() {
        val response = grpcEnroll(enrollRequest(newToken().reveal(), csr)).getOrThrow()
        val agentId = UUID.fromString(response.agentId)
        val leaf = certificates(response.certificateChainPem).single()
        assertEquals("CN=$agentId", leaf.subjectX500Principal.name)
        assertEquals(ca.caBundlePem(), response.caBundlePem)
    }

    @Test
    fun `Enroll over gRPC maps rejections to status codes`() {
        val issued = newToken()
        assertEquals(Status.Code.INVALID_ARGUMENT, code(grpcEnroll(enrollRequest("nonsense", csr))))
        assertEquals(Status.Code.INVALID_ARGUMENT, code(grpcEnroll(enrollRequest(issued.reveal(), ByteArray(3)))))
        assertUntouched(issued)
    }
}
