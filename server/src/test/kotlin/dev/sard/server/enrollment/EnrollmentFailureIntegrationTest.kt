// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import ch.qos.logback.classic.spi.ILoggingEvent
import ch.qos.logback.core.read.ListAppender
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.agents.EnrollmentGrpcService
import dev.sard.server.enrollment.EnrollmentRejectedException.Reason
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.FileCertificateAuthority
import dev.sard.server.pki.IssuedCertificate
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.PkiFixtures.resource
import io.grpc.Status
import io.grpc.StatusRuntimeException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.nio.file.Path
import java.security.SecureRandom
import java.time.Clock
import java.time.Duration
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * A collaborator failure this test simulates on purpose (a named type, not a bare
 * RuntimeException). Extends [RuntimeException], not [Exception]: a checked exception thrown
 * from inside [java.lang.reflect.Proxy] comes back wrapped in [java.lang.reflect.UndeclaredThrowableException].
 */
private class SimulatedFailure(
    message: String,
) : RuntimeException(message)

/**
 * A real [TenantSessions] wired to a [org.hibernate.SessionFactory] proxy that throws on every
 * call, standing in for a database that is down: `TenantSessions` is not designed as a seam, so
 * the fake sits one layer lower, at the interface Hibernate itself defines.
 */
private fun failingTenantSessions(): TenantSessions {
    val broken =
        java.lang.reflect.Proxy.newProxyInstance(
            TenantSessions::class.java.classLoader,
            arrayOf(org.hibernate.SessionFactory::class.java),
        ) { _, _, _ -> throw SimulatedFailure("database unavailable") } as org.hibernate.SessionFactory
    return TenantSessions(broken)
}

/** A CA whose signing fails for a reason that has nothing to do with the CSR. */
private class BrokenCertificateAuthority(
    private val delegate: CertificateAuthority,
) : CertificateAuthority by delegate {
    var failWith: String? = "internal CA failure"

    override fun issueAgentCertificate(
        csrDer: ByteArray,
        agent: AgentIdentity,
    ): IssuedCertificate {
        failWith?.let { throw SimulatedFailure(it) }
        return delegate.issueAgentCertificate(csrDer, agent)
    }
}

/**
 * Rules "Отказ базы..." (service) and "Неудачная попытка не расходует токен" (grpc): INTERNAL_RETRYABLE
 * never leaks the underlying cause, in the status text or in the server log.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, EnrollmentTestConfiguration::class)
class EnrollmentFailureIntegrationTest(
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val realSessions: TenantSessions,
    @Autowired private val ca: GatedCertificateAuthority,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @Value("\${sard.pki.dir}") private val pkiDir: Path,
) {
    private val acme = UUID.randomUUID()
    private val csr = resource("agent-p256.csr")

    /**
     * A CA object independent of the shared [ca] bean, so its `issueAgentCertificate` never
     * touches [GatedCertificateAuthority]'s mutable gate/entered state (shared, and mutated, by
     * the concurrency tests). It reads the same on-disk root key, so its fingerprint still
     * matches the tokens [tokens] issues.
     */
    private fun freshCa() = FileCertificateAuthority(pkiDir, listOf("localhost"), Clock.systemUTC(), SecureRandom())

    @BeforeTest
    fun `start at a known instant with a fresh tenant`() {
        clock.now = ENROLLMENT_NOW
        ca.gate = null
        jdbc.insertTenant(acme)
    }

    @AfterTest
    fun `drop everything of the tenant`() {
        jdbc.deleteEnrollmentTenantData(acme)
    }

    private fun ids() = UuidV7(clock, SecureRandom())

    private fun failingTokens(): EnrollmentTokens {
        val endpoint = AgentEndpoint("x:1")
        return EnrollmentTokens(failingTenantSessions(), ca, clock, SecureRandom(), ids(), endpoint)
    }

    private fun countTokens(): Int? {
        val sql = "select count(*) from enrollment_tokens where tenant_id = ?"
        return jdbc.queryForObject(sql, Int::class.java, acme)
    }

    private fun countAgents(): Int? = jdbc.countAgents(acme)

    // --- Отказ базы при создании и отзыве токена (service)

    @Test
    fun `Отказ базы при создании токена не выдаёт строку`() {
        assertFailsWith<SimulatedFailure> { failingTokens().create(acme) }
        assertEquals(0, countTokens())
    }

    @Test
    fun `Отказ базы при отзыве оставляет токен активным`() {
        val issued = tokens.create(acme, Duration.ofHours(1))
        assertFailsWith<SimulatedFailure> { failingTokens().revoke(acme, issued.id, clock.now) }
        assertEquals(EnrollmentTokenState.ACTIVE, tokens.get(acme, issued.id)?.state)
    }

    // --- Неудачная попытка не расходует токен (grpc)

    @Test
    fun `Недоступная база при поиске токена даёт INTERNAL_RETRYABLE`() {
        val issued = tokens.create(acme, Duration.ofHours(1))
        val testEnrollment = Enrollment(realSessions, failingTokens(), ca, clock, ids())
        val error = assertFailsWith<EnrollmentRejectedException> { testEnrollment.enroll(issued.reveal(), csr, "db1") }
        assertEquals(Reason.INTERNAL_RETRYABLE, error.reason)
    }

    @Test
    fun `Сбой базы внутри регистрации не расходует токен`() {
        val issued = tokens.create(acme, Duration.ofHours(1))
        val testEnrollment = Enrollment(failingTenantSessions(), tokens, ca, clock, ids())
        val error = assertFailsWith<EnrollmentRejectedException> { testEnrollment.enroll(issued.reveal(), csr, "db1") }
        assertEquals(Reason.INTERNAL_RETRYABLE, error.reason)
        assertEquals(EnrollmentTokenState.ACTIVE, tokens.get(acme, issued.id)?.state)
        assertEquals(0, countAgents())
    }

    @Test
    fun `Сбой CA не по вине CSR не расходует токен`() {
        val issued = tokens.create(acme, Duration.ofHours(1))
        val broken = BrokenCertificateAuthority(freshCa())
        val testEnrollment = Enrollment(realSessions, tokens, broken, clock, ids())
        val error = assertFailsWith<EnrollmentRejectedException> { testEnrollment.enroll(issued.reveal(), csr, "db1") }
        assertEquals(Reason.INTERNAL_RETRYABLE, error.reason)
        assertEquals(EnrollmentTokenState.ACTIVE, tokens.get(acme, issued.id)?.state)
        assertEquals(0, countAgents())
    }

    @Test
    fun `После внутренней ошибки тем же токеном можно зарегистрироваться`() {
        val issued = tokens.create(acme, Duration.ofHours(1))
        val broken = BrokenCertificateAuthority(freshCa())
        val testEnrollment = Enrollment(realSessions, tokens, broken, clock, ids())
        assertFailsWith<EnrollmentRejectedException> { testEnrollment.enroll(issued.reveal(), csr, "db1") }
        broken.failWith = null
        testEnrollment.enroll(issued.reveal(), csr, "db1")
        assertEquals(EnrollmentTokenState.USED, tokens.get(acme, issued.id)?.state)
    }

    @Test
    fun `Текст отказа INTERNAL_RETRYABLE не раскрывает внутреннюю ошибку`() {
        val issued = tokens.create(acme, Duration.ofHours(1))
        val broken = BrokenCertificateAuthority(freshCa()).apply { failWith = "disk on fire" }
        val testEnrollment = Enrollment(realSessions, tokens, broken, clock, ids())
        val service = EnrollmentGrpcService(testEnrollment, Dispatchers.Unconfined)
        val request = enrollRequest(issued.reveal(), csr)
        val appender = attachLogCapture()
        val text: String
        try {
            val error = assertFailsWith<StatusRuntimeException> { runBlocking { service.enroll(request) } }
            text = Status.fromThrowable(error).description.orEmpty()
        } finally {
            detachLogCapture(appender)
        }
        assertFalse("disk on fire" in text, text)
        val leaked =
            appender.list.any { event ->
                "disk on fire" in event.formattedMessage || causesContain(event, "disk on fire")
            }
        assertTrue(leaked)
    }

    private fun causesContain(
        event: ILoggingEvent,
        text: String,
    ): Boolean =
        event.throwableProxy?.let { proxy ->
            generateSequence(proxy) { it.cause }.any { text in (it.message ?: "") }
        } ?: false

    private fun attachLogCapture(): ListAppender<ILoggingEvent> {
        val root = org.slf4j.LoggerFactory.getLogger(org.slf4j.Logger.ROOT_LOGGER_NAME) as ch.qos.logback.classic.Logger
        val appender = ListAppender<ILoggingEvent>()
        appender.start()
        root.addAppender(appender)
        return appender
    }

    private fun detachLogCapture(appender: ListAppender<ILoggingEvent>) {
        val root = org.slf4j.LoggerFactory.getLogger(org.slf4j.Logger.ROOT_LOGGER_NAME) as ch.qos.logback.classic.Logger
        root.detachAppender(appender)
        appender.stop()
    }
}
