// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.api.captureLogs
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.PkiFixtures
import dev.sard.server.role.AgentRole
import io.grpc.Status
import org.bouncycastle.asn1.x509.BasicConstraints
import org.bouncycastle.asn1.x509.Extension
import org.bouncycastle.asn1.x509.GeneralName
import org.hibernate.Session
import org.hibernate.exception.JDBCConnectionException
import org.mockito.ArgumentMatchers
import org.mockito.Mockito
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean
import java.security.cert.X509Certificate
import java.sql.SQLException
import java.time.Duration
import java.util.Base64
import java.util.UUID
import java.util.concurrent.Callable
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val CLIENT_AUTH = "1.3.6.1.5.5.7.3.2"
private const val SAN_URI = 6
private val DAY = Duration.ofDays(1)

/** C1, rule "Продление выдаёт сертификат той же личности и роли на новый ключ", over the real server. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "sard.agent.stream.check-interval=1h"],
)
@Import(TestcontainersConfiguration::class, RenewalClockConfiguration::class)
class CertificateRenewalIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val clock: MovableClock,
    @LocalGrpcServerPort grpcPort: Int,
) {
    @MockitoSpyBean
    lateinit var sessions: TenantSessions

    @MockitoSpyBean
    lateinit var authority: CertificateAuthority

    private val fixtures = AgentGrpcFixtures(ca, enrollment, tokens, jdbc, grpcPort)
    private val harness = RenewalHarness(fixtures, jdbc, clock)
    private val tenant = fixtures.tenant()
    private val t0 = RENEWAL_T0

    @AfterTest
    fun cleanUp() {
        Mockito.reset(sessions, authority)
        clock.now = t0
        fixtures.close()
    }

    private fun leaf(agent: EnrolledAgent): X509Certificate = PkiFixtures.certificate(agent.chainPem)

    private fun uris(certificate: X509Certificate) = certificate.subjectAlternativeNames.filter { it[0] == SAN_URI }.map { it[1] as String }

    @Test
    fun `Агент получает новый сертификат той же личности на новый ключ`() {
        val x = harness.enrolledAt(tenant, t0)
        clock.now = t0 + Duration.ofDays(244)
        val k2 = fixtures.newKeys()

        val c2 = harness.renew(x, k2)

        val certificate = leaf(c2)
        assertEquals(k2.public, certificate.publicKey)
        assertEquals("CN=${x.identity.agentId}", certificate.subjectX500Principal.name)
        assertEquals(listOf(x.identity.uri(), "sard://roles/source"), uris(certificate).sorted().reversed())
        assertNotEquals(harness.serial(x), harness.serial(c2))
        assertEquals(listOf(CLIENT_AUTH), certificate.extendedKeyUsage)
        assertEquals(-1, certificate.basicConstraints)
        assertEquals(1, PkiFixtures.certificates(c2.chainPem).size)
        assertEquals(1, PkiFixtures.certificates(x.chainPem).size)
    }

    @Test
    fun `Продление записывает новый сертификат с предшественником`() {
        val x = harness.enrolledAt(tenant, t0 - DAY)
        clock.now = t0

        val c2 = harness.renew(x)

        val row = harness.row(harness.serial(c2))
        assertEquals(harness.serial(x), row["predecessor_serial"])
        assertEquals(t0, RenewalHarness.toInstant(row["issued_at"]!!))
        assertNull(row["superseded_at"])
        assertNull(harness.row(harness.serial(x))["superseded_at"])
        val owner = "select agent_id from agent_certificates where serial = ?"
        assertEquals(x.identity.agentId, jdbc.queryForObject(owner, java.util.UUID::class.java, harness.serial(c2)))
    }

    @Test
    fun `Верификатор при продлении остаётся верификатором`() {
        val v = harness.enrolledAt(tenant, t0 - DAY, AgentRole.VERIFIER)
        clock.now = t0

        val c2 = harness.renew(v)

        assertTrue("sard://roles/verifier" in uris(leaf(c2)))
        assertTrue("sard://roles/source" !in uris(leaf(c2)))
    }

    @Test
    fun `Имена и расширения CSR не попадают в продлённый сертификат`() {
        val x = harness.enrolledAt(tenant, t0 - DAY)
        clock.now = t0
        val other = fixtures.tenant()
        val y = fixtures.enrolled(other, AgentRole.VERIFIER)
        val keys = fixtures.newKeys()
        val ask =
            listOf(
                Extension(Extension.basicConstraints, true, BasicConstraints(true).encoded),
                Extension(
                    Extension.subjectAlternativeName,
                    false,
                    org.bouncycastle.asn1.x509
                        .GeneralNames(
                            arrayOf(
                                GeneralName(GeneralName.uniformResourceIdentifier, y.identity.uri()),
                                GeneralName(GeneralName.uniformResourceIdentifier, "sard://roles/verifier"),
                                GeneralName(GeneralName.dNSName, "db1.example"),
                            ),
                        ).encoded,
                ),
            )
        val before = harness.count(y)

        val c2 = harness.renew(x, keys, fixtures.csr(keys, extensions = ask))

        assertEquals(listOf(x.identity.uri(), "sard://roles/source"), uris(leaf(c2)).sorted().reversed())
        assertEquals(-1, leaf(c2).basicConstraints)
        assertEquals(emptyList(), leaf(c2).subjectAlternativeNames.filter { it[0] != SAN_URI })
        harness.assertNoNewCertificates(y, before)
    }

    @Test
    fun `До подтверждения старый сертификат продолжает работать`() {
        val x = harness.enrolledAt(tenant, t0 - DAY)
        clock.now = t0
        harness.renew(x)

        assertEquals(x.identity.agentId.toString(), harness.register(x).agentId)
    }

    // --- Срок сертификата

    @Test
    fun `Без настройки сертификат агента выдаётся ровно на 365 суток`() {
        val x = harness.enrolledAt(tenant, t0)

        val certificate = leaf(x)
        assertEquals(t0, certificate.notBefore.toInstant())
        assertEquals(t0 + Duration.ofDays(365), certificate.notAfter.toInstant())
        assertEquals(certificate.notAfter.toInstant(), RenewalHarness.toInstant(harness.row(harness.serial(x))["not_after"]!!))
    }

    // --- CSR обязан нести новый допустимый ключ

    @Test
    fun `CSR на ключе текущего сертификата отклоняется`() {
        val x = harness.enrolledAt(tenant, t0 - DAY)
        clock.now = t0
        val before = harness.count(x)

        val refusal = harness.refused { harness.renew(x, x.keys) }

        assertEquals(CallRefusal(Status.Code.INVALID_ARGUMENT, listOf("CSR_KEY_REUSED"), listOf("sard.dev")), refusal)
        harness.assertNoNewCertificates(x, before)
    }

    @Test
    fun `Негодный CSR отклоняется как CSR_INVALID`() {
        val x = harness.enrolledAt(tenant, t0 - DAY)
        clock.now = t0
        val before = harness.count(x)
        val bad =
            mapOf(
                "пустым" to ByteArray(0),
                "из 64 случайных байт" to ByteArray(64) { (it * 7 + 3).toByte() },
                "CSR P-256 с испорченной подписью" to PkiFixtures.resource("agent-bad-signature.csr"),
                "CSR на ключе RSA 2048" to PkiFixtures.resource("agent-rsa.csr"),
                "CSR на ключе Ed25519" to PkiFixtures.resource("agent-ed25519.csr"),
                "CSR на ключе ECDSA P-521" to PkiFixtures.resource("agent-p521.csr"),
            )
        for ((name, csr) in bad) {
            val refusal = harness.refused { harness.renew(x, csr = csr) }
            assertEquals(CallRefusal(Status.Code.INVALID_ARGUMENT, listOf("CSR_INVALID"), listOf("sard.dev")), refusal, name)
        }
        harness.assertNoNewCertificates(x, before)
    }

    @Test
    fun `Недопустимый CSR проверяется раньше лимита частоты`() {
        val x = harness.enrolledAt(tenant, t0 - DAY)
        clock.now = t0
        harness.renew(x)
        clock.now = t0 + Duration.ofMinutes(10)

        val refusal = harness.refused { harness.renew(x, csr = ByteArray(0)) }

        assertEquals(listOf("CSR_INVALID"), refusal.reasons)
    }

    @Test
    fun `Отклонённый CSR не расходует лимит частоты`() {
        val x = harness.enrolledAt(tenant, t0)
        clock.now = t0 + Duration.ofHours(2)
        harness.refused { harness.renew(x, csr = ByteArray(0)) }

        val c2 = harness.renew(x)

        assertEquals(2, harness.count(c2))
    }

    // --- Не больше одного выпуска на агента за действующий интервал

    @Test
    fun `Второй выпуск в пределах интервала отклоняется с задержкой повтора`() {
        val x = harness.enrolledAt(tenant, t0)
        clock.now = t0 + Duration.ofHours(2)
        val c2 = harness.renew(x)
        clock.now = t0 + Duration.ofHours(2) + Duration.ofMinutes(20)
        val before = harness.count(x)

        val refusal = harness.refused { harness.renew(x) }

        assertEquals(CallRefusal(Status.Code.RESOURCE_EXHAUSTED, listOf("RENEWAL_TOO_FREQUENT"), listOf("sard.dev"), 2400), refusal)
        harness.assertNoNewCertificates(x, before)
        assertNull(harness.supersededAt(harness.serial(c2)))
    }

    @Test
    fun `По окончании интервала выпуск снова разрешён`() {
        val x = harness.enrolledAt(tenant, t0)
        clock.now = t0 + Duration.ofHours(2)
        harness.renew(x)
        clock.now = t0 + Duration.ofHours(3)

        val c3 = harness.renew(x)

        assertEquals(harness.serial(x), harness.row(harness.serial(c3))["predecessor_serial"])
    }

    @Test
    fun `Выпуск при регистрации тоже считается`() {
        val x = harness.enrolledAt(tenant, t0)
        clock.now = t0 + Duration.ofMinutes(30)

        val refusal = harness.refused { harness.renew(x) }

        assertEquals(Status.Code.RESOURCE_EXHAUSTED, refusal.code)
        assertEquals(1800L, refusal.retryDelaySeconds)
    }

    @Test
    fun `Задержка повтора округляется вверх до целых секунд`() {
        val x = harness.enrolledAt(tenant, t0)
        clock.now = t0 + Duration.ofMinutes(59) + Duration.ofSeconds(59) + Duration.ofMillis(500)

        val refusal = harness.refused { harness.renew(x) }

        assertEquals(1L, refusal.retryDelaySeconds)
    }

    @Test
    fun `Действующий интервал по умолчанию час`() {
        val x = harness.enrolledAt(tenant, t0)
        clock.now = t0 + Duration.ofHours(1) - Duration.ofSeconds(1)
        assertEquals(listOf("RENEWAL_TOO_FREQUENT"), harness.refused { harness.renew(x) }.reasons)
        clock.now = t0 + Duration.ofHours(1)
        harness.renew(x)
    }

    @Test
    fun `Одновременные продления дают ровно один выпуск`() {
        val x = harness.enrolledAt(tenant, t0)
        clock.now = t0 + Duration.ofHours(2)
        val pool = Executors.newFixedThreadPool(2)
        val go = CountDownLatch(1)
        val outcomes =
            try {
                val calls =
                    List(2) {
                        pool.submit(
                            Callable {
                                go.await()
                                runCatching { harness.renew(x) }
                            },
                        )
                    }
                go.countDown()
                calls.map { it.get(20, TimeUnit.SECONDS) }
            } finally {
                pool.shutdownNow()
            }

        assertEquals(1, outcomes.count { it.isSuccess })
        val failure = outcomes.single { it.isFailure }.exceptionOrNull() as io.grpc.StatusRuntimeException
        assertEquals(listOf("RENEWAL_TOO_FREQUENT"), callRefusalOf(failure).reasons)
        assertEquals(2, harness.count(x))
    }

    @Test
    fun `Лимит частоты у каждого агента свой`() {
        val x = harness.enrolledAt(tenant, t0)
        val y = fixtures.enrolled(tenant)
        clock.now = t0 + Duration.ofHours(2)

        harness.renew(x)
        clock.now += Duration.ofMinutes(1)
        harness.renew(y)

        assertEquals(2, harness.count(x))
        assertEquals(2, harness.count(y))
    }

    // --- Сбой при выпуске ничего не меняет

    private fun <T : Any> anything(placeholder: T): T {
        ArgumentMatchers.any<Any>()
        return placeholder
    }

    private fun failedRenewalChangesNothing(breakDown: () -> Unit) {
        val x = harness.enrolledAt(tenant, t0 - DAY)
        clock.now = t0
        val c2 = harness.renew(x)
        clock.now = t0 + Duration.ofHours(1)
        val before = harness.count(x)
        breakDown()

        val refusal = harness.refused { harness.renew(x) }

        assertEquals(CallRefusal(Status.Code.UNAVAILABLE, listOf("INTERNAL_RETRYABLE"), listOf("sard.dev")), refusal)
        harness.assertNoNewCertificates(x, before)
        assertNull(harness.supersededAt(harness.serial(c2)))
        Mockito.reset(sessions, authority)
        harness.renew(x)
    }

    @Test
    fun `Сбой записи в базу при продлении не выпускает сертификат и не расходует лимит`() {
        failedRenewalChangesNothing {
            val failure = JDBCConnectionException("down", SQLException("down"))
            Mockito.doThrow(failure).`when`(sessions).inTenant(anything(UUID.randomUUID()), anything<(Session) -> Any?> { null })
        }
    }

    @Test
    fun `Сбой подписи сертификата CA при продлении не выпускает сертификат и не расходует лимит`() {
        failedRenewalChangesNothing {
            Mockito
                .doThrow(IllegalStateException("signer down"))
                .`when`(authority)
                .issueAgentCertificate(anything(ByteArray(0)), anything(AgentIdentity(tenant, tenant)), anything(AgentRole.SOURCE))
        }
    }

    // --- Лог

    @Test
    fun `Продление и подтверждение пишут в лог серийные номера, но не содержимое`() {
        val x = harness.enrolledAt(tenant, t0 - DAY)
        clock.now = t0
        val keys = fixtures.newKeys()
        val csr = fixtures.csr(keys)
        lateinit var c2: EnrolledAgent

        val logs =
            captureLogs {
                c2 = harness.renew(x, keys, csr)
                harness.confirm(c2)
            }

        val renewed = logs.single { "certificate renewed" in it }
        for (part in listOf("agent=${x.identity.agentId}", "tenant=$tenant", "old=${harness.serial(x)}", "new=${harness.serial(c2)}")) {
            assertTrue(part in renewed, renewed)
        }
        assertTrue("notAfter=${leaf(c2).notAfter.toInstant()}" in renewed, renewed)
        val confirmed = logs.single { "certificate confirmed" in it }
        assertTrue("agent=${x.identity.agentId}" in confirmed && "superseded=${harness.serial(x)}" in confirmed, confirmed)
        val body = (c2.chainPem.lines() + Base64.getEncoder().encodeToString(csr)).filter { it.isNotBlank() && !it.startsWith("-----") }
        assertFalse(logs.any { "BEGIN" in it || body.any { piece -> piece in it } }, logs.toString())
    }

    @Test
    fun `Отказ продления пишет в лог причину, агента и серийный номер`() {
        val x = harness.enrolledAt(tenant, t0)
        clock.now = t0 + Duration.ofMinutes(5)

        val logs = captureLogs { harness.refused { harness.renew(x) } }

        val line = logs.single { "certificate renewal refused" in it }
        assertTrue("reason=RENEWAL_TOO_FREQUENT" in line, line)
        assertTrue("agent=${x.identity.agentId}" in line && "serial=${harness.serial(x)}" in line, line)
    }
}
