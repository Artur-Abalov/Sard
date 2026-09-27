// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import ch.qos.logback.classic.spi.ILoggingEvent
import ch.qos.logback.core.read.ListAppender
import com.google.protobuf.ByteString
import dev.sard.proto.agent.v1.EnrollRequest
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.agents.EnrollmentGrpcService
import dev.sard.server.pki.CaFingerprint
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.PkiFixtures.resource
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.protobuf.StatusProto
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.security.SecureRandom
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertFalse

private val NOW: Instant = Instant.parse("2026-09-27T10:00:00Z")

/** Rule "Строка токена не появляется в логах, текстах ошибок и ответах". */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, EnrollmentTestConfiguration::class)
class EnrollmentTokenLeakageIntegrationTest(
    @Autowired private val enrollment: Enrollment,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val ca: GatedCertificateAuthority,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val acme = UUID.randomUUID()
    private val csr = resource("agent-p256.csr")
    private val service = EnrollmentGrpcService(enrollment, Dispatchers.Unconfined)

    @BeforeTest
    fun `start at a known instant with a fresh tenant`() {
        clock.now = NOW
        ca.gate = null
        jdbc.update("insert into tenants (id, name) values (?, ?)", acme, "acme-$acme")
    }

    @AfterTest
    fun `drop everything of the tenant`() {
        for (table in listOf("agent_certificates", "enrollment_tokens", "agents")) {
            jdbc.update("delete from $table where tenant_id = ?", acme)
        }
        jdbc.update("delete from tenants where id = ?", acme)
    }

    private fun secretsOf(token: String): List<String> = listOf(token, token.removePrefix("sard_").substringBefore('.'))

    private fun capture(block: () -> Unit): List<String> {
        val root = org.slf4j.LoggerFactory.getLogger(org.slf4j.Logger.ROOT_LOGGER_NAME) as ch.qos.logback.classic.Logger
        val appender = ListAppender<ILoggingEvent>()
        appender.start()
        root.addAppender(appender)
        try {
            block()
        } finally {
            root.detachAppender(appender)
            appender.stop()
        }
        return appender.list.flatMap { event ->
            listOfNotNull(event.formattedMessage) +
                generateSequence(event.throwableProxy) { it.cause }.mapNotNull { it.message }
        }
    }

    private fun assertNoLeak(
        logLines: List<String>,
        secrets: List<String>,
    ) {
        val joined = logLines.joinToString("\n")
        for (secret in secrets) assertFalse(secret in joined, "leaked '$secret' in log: $joined")
    }

    @Test
    fun `Строка токена не попадает в лог при создании токена`() {
        var reveal = ""
        val log = capture { reveal = tokens.create(acme).reveal() }
        assertNoLeak(log, secretsOf(reveal))
    }

    @Test
    fun `Строка токена не попадает в лог при отзыве`() {
        val issued = tokens.create(acme, Duration.ofHours(1))
        val log = capture { tokens.revoke(acme, issued.id, clock.now) }
        assertNoLeak(log, secretsOf(issued.reveal()))
    }

    @Test
    fun `Строка токена не попадает в лог при успешной регистрации`() {
        val issued = tokens.create(acme, Duration.ofHours(1))
        val log = capture { runBlocking { service.enroll(request(issued.reveal(), csr, "db1")) } }
        assertNoLeak(log, secretsOf(issued.reveal()))
    }

    @Test
    fun `Строка токена не попадает ни в лог, ни в текст отказа`() {
        for ((name, requestFor) in rejectionRequests()) {
            val issued = tokens.create(acme, Duration.ofHours(1))
            val secrets = secretsOf(issued.reveal())
            var status: StatusRuntimeException? = null
            val log =
                capture {
                    val result = runCatching { runBlocking { service.enroll(requestFor(issued)) } }
                    status = result.exceptionOrNull() as? StatusRuntimeException
                }
            assertNoLeak(log, secrets)
            val rpcStatus = checkNotNull(status) { "$name did not fail" }
            val description = Status.fromThrowable(rpcStatus).description.orEmpty()
            val text = description + StatusProto.fromThrowable(rpcStatus).toString()
            for (secret in secrets) assertFalse(secret in text, "$name leaked '$secret' in status: $text")
        }
    }

    private fun rejectionRequests(): Map<String, (IssuedEnrollmentToken) -> EnrollRequest> =
        mapOf(
            "TOKEN_MALFORMED" to { _: IssuedEnrollmentToken -> request("not-a-token", csr, "db1") },
            "TOKEN_FOREIGN_CA" to { issued: IssuedEnrollmentToken ->
                val secret = EnrollmentToken.parse(issued.reveal()).secret
                request(EnrollmentToken(secret, CaFingerprint("0".repeat(64))).encode(), csr, "db1")
            },
            "TOKEN_UNKNOWN" to { _: IssuedEnrollmentToken ->
                request(EnrollmentToken(EnrollmentSecret.random(SecureRandom()), ca.fingerprint()).encode(), csr, "db1")
            },
            "TOKEN_USED" to { issued: IssuedEnrollmentToken ->
                enrollment.enroll(issued.reveal(), csr, "db1")
                request(issued.reveal(), resource("agent-p384.csr"), "db2")
            },
            "TOKEN_REVOKED" to { issued: IssuedEnrollmentToken ->
                tokens.revoke(acme, issued.id, clock.now)
                request(issued.reveal(), csr, "db1")
            },
            "TOKEN_EXPIRED" to { issued: IssuedEnrollmentToken ->
                clock.now = NOW + Duration.ofHours(2)
                request(issued.reveal(), csr, "db1")
            },
            "HOSTNAME_INVALID" to { issued: IssuedEnrollmentToken -> request(issued.reveal(), csr, "") },
            "CSR_INVALID" to { issued: IssuedEnrollmentToken ->
                request(issued.reveal(), "garbage".toByteArray(), "db1")
            },
            "INTERNAL_RETRYABLE" to { issued: IssuedEnrollmentToken -> request(issued.reveal(), ByteArray(0), "") },
        )

    private fun request(
        token: String,
        csrDer: ByteArray,
        hostname: String,
    ) = EnrollRequest
        .newBuilder()
        .setEnrollmentToken(token)
        .setCsrDer(ByteString.copyFrom(csrDer))
        .setHostname(hostname)
        .build()
}
