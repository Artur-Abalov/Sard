// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.AgentServiceGrpc
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.api.captureLogs
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.stub.ClientCalls
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
import java.sql.SQLException
import java.time.Duration
import java.util.UUID
import java.util.concurrent.Callable
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val HOUR = Duration.ofHours(1)
private val DAY = Duration.ofDays(1)

/** C1, rules "Подтверждение новым сертификатом вытесняет старый" and "Неподтверждённый сертификат годится только для подтверждения". */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "sard.agent.stream.check-interval=1h"],
)
@Import(TestcontainersConfiguration::class, RenewalClockConfiguration::class)
class CertificateConfirmationIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val clock: MovableClock,
    @LocalGrpcServerPort grpcPort: Int,
) {
    @MockitoSpyBean
    lateinit var sessions: TenantSessions

    private val fixtures = AgentGrpcFixtures(ca, enrollment, tokens, jdbc, grpcPort)
    private val harness = RenewalHarness(fixtures, jdbc, clock)
    private val tenant = fixtures.tenant()
    private val t0 = RENEWAL_T0

    @AfterTest
    fun cleanUp() {
        Mockito.reset(sessions)
        clock.now = t0
        fixtures.close()
    }

    /** X enrolled a day before T0, renewed C1 at T0 and got C2. */
    private fun renewed(): Pair<EnrolledAgent, EnrolledAgent> {
        val c1 = harness.enrolledAt(tenant, t0 - DAY)
        clock.now = t0
        return c1 to harness.renew(c1)
    }

    private fun confirmedPair(): Pair<EnrolledAgent, EnrolledAgent> = renewed().also { (_, c2) -> harness.confirm(c2) }

    private fun <T : Any> anything(placeholder: T): T {
        ArgumentMatchers.any<Any>()
        return placeholder
    }

    // --- Подтверждение новым сертификатом вытесняет старый

    @Test
    fun `Подтверждение по новому сертификату вытесняет старый`() {
        val (c1, c2) = renewed()

        harness.confirm(c2)

        assertEquals(t0, harness.supersededAt(harness.serial(c1)))
        assertNull(harness.supersededAt(harness.serial(c2)))
    }

    @Test
    fun `После подтверждения агент работает с новым сертификатом под тем же идентификатором`() {
        val (_, c2) = confirmedPair()
        val agents = "select count(*) from agents where tenant_id = ?"
        val before = jdbc.queryForObject(agents, Int::class.java, tenant)

        val response = harness.register(c2)

        assertEquals(c2.identity.agentId.toString(), response.agentId)
        assertEquals(before, jdbc.queryForObject(agents, Int::class.java, tenant))
        val role = jdbc.queryForObject("select role from agents where id = ?", String::class.java, c2.identity.agentId)
        assertEquals("source", role)
    }

    @Test
    fun `Вытесненный сертификат отклоняется каждым методом`() {
        val (c1, _) = confirmedPair()
        val before = harness.count(c1)
        val calls: Map<String, () -> Unit> =
            mapOf(
                "Register" to { harness.register(c1) },
                "RenewCertificate" to { harness.renew(c1) },
                "ConfirmCertificate" to { harness.confirm(c1) },
            )
        for ((_, call) in calls) harness.assertRefusedBy("CERT_SUPERSEDED", call)
        val connect = harness.connectRefusal(c1)
        assertEquals(CallRefusal(Status.Code.UNAUTHENTICATED, listOf("CERT_SUPERSEDED"), listOf("sard.dev")), connect)
        harness.assertNoNewCertificates(c1, before)
    }

    @Test
    fun `Повторное подтверждение ничего не меняет`() {
        val (c1, c2) = renewed()
        harness.confirm(c2)
        clock.now = t0 + Duration.ofMinutes(5)

        harness.confirm(c2)

        assertEquals(t0, harness.supersededAt(harness.serial(c1)))
    }

    @Test
    fun `Подтверждение сертификата без предшественника ничего не меняет`() {
        val c1 = harness.enrolledAt(tenant, t0 - DAY)
        clock.now = t0
        val before = jdbc.queryForList("select * from agent_certificates where agent_id = ?", c1.identity.agentId)

        harness.confirm(c1)

        assertEquals(before, jdbc.queryForList("select * from agent_certificates where agent_id = ?", c1.identity.agentId))
    }

    @Test
    fun `Одновременные подтверждения ставят одно время вытеснения`() {
        val (c1, c2) = renewed()
        val pool = Executors.newFixedThreadPool(2)
        val go = CountDownLatch(1)
        try {
            val calls =
                List(2) {
                    pool.submit(
                        Callable {
                            go.await()
                            harness.confirm(c2)
                        },
                    )
                }
            go.countDown()
            calls.forEach { it.get(20, TimeUnit.SECONDS) }
        } finally {
            pool.shutdownNow()
        }

        assertEquals(t0, harness.supersededAt(harness.serial(c1)))
        val sql = "select count(*) from agent_certificates where superseded_at is not null and agent_id = ?"
        assertEquals(1, jdbc.queryForObject(sql, Int::class.java, c1.identity.agentId))
    }

    @Test
    fun `Сбой базы при подтверждении не вытесняет старый сертификат`() {
        val (c1, c2) = renewed()
        val failure = JDBCConnectionException("down", SQLException("down"))
        Mockito.doThrow(failure).`when`(sessions).inTenant(anything(UUID.randomUUID()), anything<(Session) -> Any?> { null })

        val refusal = harness.refused { harness.confirm(c2) }

        assertEquals(CallRefusal(Status.Code.UNAVAILABLE, listOf("INTERNAL_RETRYABLE"), listOf("sard.dev")), refusal)
        assertNull(harness.supersededAt(harness.serial(c1)))
        Mockito.reset(sessions)
        harness.confirm(c2)
        assertEquals(t0, harness.supersededAt(harness.serial(c1)))
    }

    @Test
    fun `Подтверждение без клиентского сертификата отклоняется`() {
        val refusal =
            harness.refused {
                val channel = fixtures.anonymousChannel()
                ClientCalls.blockingUnaryCall(
                    channel,
                    AgentServiceGrpc.getConfirmCertificateMethod(),
                    io.grpc.CallOptions.DEFAULT
                        .withDeadlineAfter(10, TimeUnit.SECONDS),
                    dev.sard.proto.agent.v1.ConfirmCertificateRequest
                        .getDefaultInstance(),
                )
            }
        assertEquals(CallRefusal(Status.Code.UNAUTHENTICATED, listOf("CERT_MISSING"), listOf("sard.dev")), refusal)
    }

    @Test
    fun `Отзыв важнее вытеснения`() {
        val (c1, _) = confirmedPair()
        jdbc.update("update agent_certificates set revoked_at = ? where serial = ?", java.sql.Timestamp.from(t0), harness.serial(c1))

        harness.assertRefusedBy("CERT_REVOKED") { harness.register(c1) }
    }

    @Test
    fun `Вытеснение важнее истечения`() {
        val (c1, _) = confirmedPair()
        clock.now = RenewalHarness.toInstant(harness.row(harness.serial(c1))["not_after"]!!)

        harness.assertRefusedBy("CERT_SUPERSEDED") { harness.register(c1) }
    }

    @Test
    fun `Строка лога отказа CERT_SUPERSEDED называет агента и серийный номер`() {
        val (c1, _) = confirmedPair()

        val logs = captureLogs { harness.assertRefusedBy("CERT_SUPERSEDED") { harness.register(c1) } }

        val line = logs.single { "reason=CERT_SUPERSEDED" in it }
        assertTrue("agent=${c1.identity.agentId}" in line, line)
        assertTrue("serial=${harness.serial(c1)}" in line, line)
        assertNoCertificateMaterial(logs, c1)
    }

    // --- Неподтверждённый сертификат годится только для подтверждения

    @Test
    fun `Неподтверждённый сертификат отклоняется всеми методами, кроме подтверждения`() {
        val (c1, c2) = renewed()
        val before = harness.count(c1)
        val calls: Map<String, () -> Unit> =
            mapOf(
                "Register" to { harness.register(c2) },
                "RenewCertificate" to { harness.renew(c2) },
            )
        for ((_, call) in calls) harness.assertRefusedBy("CERT_NOT_CONFIRMED", call)
        val connect = harness.connectRefusal(c2)
        assertEquals(CallRefusal(Status.Code.UNAUTHENTICATED, listOf("CERT_NOT_CONFIRMED"), listOf("sard.dev")), connect)
        harness.assertNoNewCertificates(c1, before)
    }

    @Test
    fun `Повторное продление вытесняет неподтверждённый сертификат`() {
        val c1 = harness.enrolledAt(tenant, t0 - DAY)
        clock.now = t0
        val c2 = harness.renew(c1)
        clock.now = t0 + HOUR

        val c3 = harness.renew(c1)

        assertEquals(t0 + HOUR, harness.supersededAt(harness.serial(c2)))
        assertNull(harness.supersededAt(harness.serial(c1)))
        assertNull(harness.supersededAt(harness.serial(c3)))
    }

    @Test
    fun `Заменённый неподтверждённый сертификат нельзя подтвердить`() {
        val c1 = harness.enrolledAt(tenant, t0 - DAY)
        clock.now = t0
        val c2 = harness.renew(c1)
        clock.now = t0 + HOUR
        harness.renew(c1)

        harness.assertRefusedBy("CERT_SUPERSEDED") { harness.confirm(c2) }

        assertNull(harness.supersededAt(harness.serial(c1)))
    }

    @Test
    fun `Подтверждение заменяющего сертификата вытесняет исходный`() {
        val c1 = harness.enrolledAt(tenant, t0 - DAY)
        clock.now = t0
        harness.renew(c1)
        clock.now = t0 + HOUR
        val c3 = harness.renew(c1)

        harness.confirm(c3)

        assertEquals(t0 + HOUR, harness.supersededAt(harness.serial(c1)))
    }

    private fun assertNoCertificateMaterial(
        logs: List<String>,
        agent: EnrolledAgent,
    ) {
        val body = agent.chainPem.lines().filter { it.isNotBlank() && !it.startsWith("-----") }
        assertFalse(logs.any { "BEGIN" in it }, logs.toString())
        assertFalse(logs.any { line -> body.any { it in line } }, logs.toString())
    }
}
