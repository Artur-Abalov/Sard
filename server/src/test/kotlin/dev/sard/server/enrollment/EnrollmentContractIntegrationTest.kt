// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import com.google.rpc.ErrorInfo
import dev.sard.proto.agent.v1.EnrollRequest
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.agents.EnrollmentGrpcService
import dev.sard.server.enrollment.EnrollmentRejectedException.Reason
import dev.sard.server.pki.CaFingerprint
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.PkiFixtures.certificates
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
import java.sql.Timestamp
import java.time.Duration
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

/**
 * Rules "Успешная регистрация выдаёт агенту идентичность", "hostname — от 1 до 253 символов",
 * "Отказы по токену — окончательный контракт" and "Неудачная попытка не расходует токен".
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, EnrollmentTestConfiguration::class)
class EnrollmentContractIntegrationTest(
    @Autowired private val enrollment: Enrollment,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val ca: GatedCertificateAuthority,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val acme = UUID.randomUUID()
    private val defaultTenant = dev.sard.server.extension.TenantResolver.DEFAULT_TENANT_ID
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
        for (table in listOf("agent_certificates", "enrollment_tokens", "agents")) {
            jdbc.update("delete from $table where tenant_id in (?, ?)", acme, defaultTenant)
        }
        jdbc.update("delete from tenants where id = ?", acme)
    }

    private fun newToken(ttl: Duration = ENROLLMENT_TOKEN_TTL) = tokens.create(acme, ttl)

    private fun countAgents(tenant: UUID = acme) = jdbc.countAgents(tenant)

    // --- Успешная регистрация выдаёт агенту идентичность в тенанте токена

    @Test
    fun `Регистрация по активному токену выдаёт агенту сертификат`() {
        val issued = newToken()
        val enrolled = enrollment.enroll(issued.reveal(), csr, "db1")
        val leaf = certificates(enrolled.certificateChainPem).single()
        val uri = "sard://tenants/$acme/agents/${enrolled.agentId}"
        assertEquals(listOf(listOf<Any>(6, uri)), leaf.subjectAlternativeNames.map { it.toList() })
        assertEquals(ca.caBundlePem(), enrolled.caBundlePem)
    }

    @Test
    fun `Агент попадает в тенант токена, а не в тенант по умолчанию`() {
        val issued = newToken()
        enrollment.enroll(issued.reveal(), csr, "db1")
        assertEquals(1, countAgents(acme))
        assertEquals(0, countAgents(defaultTenant))
    }

    @Test
    fun `Зарегистрированный агент тенанта A невидим в тенанте B`() {
        val issued = newToken()
        val enrolled = enrollment.enroll(issued.reveal(), csr, "db1")
        val tenantB = UUID.randomUUID()
        jdbc.update("insert into tenants (id, name) values (?, ?)", tenantB, "b-$tenantB")
        try {
            assertEquals(
                0,
                jdbc.queryForObject(
                    "select count(*) from agents where tenant_id = ? and id = ?",
                    Int::class.java,
                    tenantB,
                    enrolled.agentId,
                ),
            )
        } finally {
            jdbc.update("delete from tenants where id = ?", tenantB)
        }
    }

    @Test
    fun `Успешная регистрация делает токен использованным`() {
        val issued = newToken()
        val enrolled = enrollment.enroll(issued.reveal(), csr, "db1")
        val row = jdbc.queryForMap("select used_at, agent_id from enrollment_tokens where id = ?", issued.id)
        assertEquals(ENROLLMENT_NOW, (row["used_at"] as Timestamp).toInstant())
        assertEquals(enrolled.agentId, row["agent_id"])
    }

    @Test
    fun `Повторная регистрация того же хоста новым токеном создаёт второго агента`() {
        val first = newToken()
        val x = enrollment.enroll(first.reveal(), csr, "db1")
        val second = newToken()
        val y = enrollment.enroll(second.reveal(), resource("agent-p384.csr"), "db1")
        assertNotEquals(x.agentId, y.agentId)
        val sql = "select count(*) from agents where tenant_id = ? and hostname = ?"
        assertEquals(2, jdbc.queryForObject(sql, Int::class.java, acme, "db1"))
    }

    // --- hostname — от 1 до 253 символов

    @Test
    fun `Регистрация с пустым hostname отклоняется и не расходует токен`() {
        val issued = newToken()
        assertEquals(Reason.HOSTNAME_INVALID, rejectionReason { enrollment.enroll(issued.reveal(), csr, "") })
        assertEquals(0, countAgents())
    }

    @Test
    fun `Hostname из 253 символов принимается`() {
        val issued = newToken()
        val hostname = "a".repeat(253)
        val enrolled = enrollment.enroll(issued.reveal(), csr, hostname)
        val row = jdbc.queryForObject("select hostname from agents where id = ?", String::class.java, enrolled.agentId)
        assertEquals(hostname, row)
    }

    @Test
    fun `Hostname из 254 символов отклоняется и не расходует токен`() {
        val issued = newToken()
        val reason = rejectionReason { enrollment.enroll(issued.reveal(), csr, "a".repeat(254)) }
        assertEquals(Reason.HOSTNAME_INVALID, reason)
        assertEquals(0, countAgents())
    }

    @Test
    fun `Hostname с управляющим символом отклоняется и не расходует токен`() {
        for (hostname in listOf("db1\u0000", "db\n1", "db1\u007f")) {
            val issued = newToken()
            val reason = rejectionReason { enrollment.enroll(issued.reveal(), csr, hostname) }
            assertEquals(Reason.HOSTNAME_INVALID, reason, hostname)
        }
        assertEquals(0, countAgents())
    }

    @Test
    fun `Отказ по токену важнее невалидного hostname`() {
        val issued = newToken()
        enrollment.enroll(issued.reveal(), csr, "db1")
        assertEquals(Reason.TOKEN_USED, rejectionReason { enrollment.enroll(issued.reveal(), csr, "") })
    }

    @Test
    fun `Невалидный hostname важнее невалидного CSR`() {
        val issued = newToken()
        val reason = rejectionReason { enrollment.enroll(issued.reveal(), "garbage".toByteArray(), "") }
        assertEquals(Reason.HOSTNAME_INVALID, reason)
        assertEquals(0, countAgents())
    }

    // --- Отказы по токену — окончательный контракт

    @Test
    fun `Неразбираемая строка токена отклоняется как TOKEN_MALFORMED`() {
        val secret = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"
        val fingerprint = "8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f"
        val cases =
            listOf(
                "",
                "$secret.$fingerprint",
                "SARD_$secret.$fingerprint",
                "sard_$secret.$fingerprint.x",
                "sard_${secret.dropLast(1)}.$fingerprint",
                "sard_${secret}A=.$fingerprint",
                "sard_${secret.dropLast(1)}+.$fingerprint",
                "sard_${secret.dropLast(1)}9.$fingerprint",
                "sard_$secret.${fingerprint.dropLast(1)}",
                "sard_$secret.${fingerprint.uppercase()}",
                " sard_$secret.$fingerprint",
                "sard_$secret.$fingerprint\n",
            )
        for (case in cases) {
            assertEquals(Reason.TOKEN_MALFORMED, rejectionReason { enrollment.enroll(case, csr, "db1") }, case)
        }
        assertEquals(0, countAgents())
    }

    @Test
    fun `Пустой запрос регистрации отклоняется как TOKEN_MALFORMED`() {
        val service = EnrollmentGrpcService(enrollment, Dispatchers.Unconfined)
        val error =
            assertFailsWith<StatusRuntimeException> {
                runBlocking {
                    service.enroll(EnrollRequest.getDefaultInstance())
                }
            }
        assertEquals(Status.Code.INVALID_ARGUMENT, Status.fromThrowable(error).code)
        val details = StatusProto.fromThrowable(error)?.detailsList.orEmpty()
        val info = details.single { it.`is`(ErrorInfo::class.java) }.unpack(ErrorInfo::class.java)
        assertEquals("TOKEN_MALFORMED", info.reason)
        assertEquals(0, countAgents())
    }

    @Test
    fun `Испорченная копия настоящего токена не трогает его`() {
        val issued = newToken()
        val parsed = EnrollmentToken.parse(issued.reveal())
        // Same secret, an upper-cased (and thus malformed) fingerprint: derived from the public
        // token string, not EnrollmentSecret.encoded() (internal — see docs/adr/0006, this class
        // is not exempt from that boundary).
        val secretText = issued.reveal().substringAfter("sard_").substringBefore('.')
        val corrupted = "sard_$secretText.${parsed.fingerprint.hex.uppercase()}"
        assertEquals(Reason.TOKEN_MALFORMED, rejectionReason { enrollment.enroll(corrupted, csr, "db1") })
        val row = jdbc.queryForMap("select used_at, agent_id from enrollment_tokens where id = ?", issued.id)
        assertEquals(mapOf<String, Any?>("used_at" to null, "agent_id" to null), row)
    }

    @Test
    fun `Токен с отпечатком чужого CA отклоняется как TOKEN_FOREIGN_CA`() {
        val issued = newToken()
        val parsed = EnrollmentToken.parse(issued.reveal())
        val foreign = EnrollmentToken(parsed.secret, CaFingerprint("0".repeat(64))).encode()
        assertEquals(Reason.TOKEN_FOREIGN_CA, rejectionReason { enrollment.enroll(foreign, csr, "db1") })
        assertEquals(0, countAgents())
        val row = jdbc.queryForMap("select used_at, agent_id from enrollment_tokens where id = ?", issued.id)
        assertEquals(mapOf<String, Any?>("used_at" to null, "agent_id" to null), row)
    }

    @Test
    fun `Неизвестный токен отклоняется как TOKEN_UNKNOWN`() {
        val randomSecret = EnrollmentSecret.random(java.security.SecureRandom())
        val unknown = EnrollmentToken(randomSecret, ca.fingerprint()).encode()
        assertEquals(Reason.TOKEN_UNKNOWN, rejectionReason { enrollment.enroll(unknown, csr, "db1") })
        assertEquals(0, countAgents())
    }

    @Test
    fun `Использованный токен отклоняется как TOKEN_USED`() {
        val issued = newToken()
        val enrolled = enrollment.enroll(issued.reveal(), csr, "db1")
        val secondCsr = resource("agent-p384.csr")
        assertEquals(Reason.TOKEN_USED, rejectionReason { enrollment.enroll(issued.reveal(), secondCsr, "db2") })
        assertEquals(1, countAgents())
        val sql = "select agent_id from enrollment_tokens where id = ?"
        assertEquals(enrolled.agentId, jdbc.queryForObject(sql, UUID::class.java, issued.id))
    }

    @Test
    fun `Истёкший токен отклоняется как TOKEN_EXPIRED`() {
        val issued = newToken(Duration.ofMinutes(5))
        clock.now = ENROLLMENT_NOW + Duration.ofMinutes(5)
        assertEquals(Reason.TOKEN_EXPIRED, rejectionReason { enrollment.enroll(issued.reveal(), csr, "db1") })
        assertEquals(0, countAgents())
        assertEquals(EnrollmentTokenState.EXPIRED, tokens.get(acme, issued.id)?.state)
    }

    @Test
    fun `Токен за миллисекунду до срока ещё регистрирует агента`() {
        val issued = newToken(Duration.ofMinutes(5))
        clock.now = ENROLLMENT_NOW + Duration.ofMinutes(5) - Duration.ofMillis(1)
        enrollment.enroll(issued.reveal(), csr, "db1")
        assertEquals(1, countAgents())
    }

    @Test
    fun `Отозванный токен отклоняется как TOKEN_REVOKED`() {
        val issued = newToken()
        tokens.revoke(acme, issued.id, clock.now)
        assertEquals(Reason.TOKEN_REVOKED, rejectionReason { enrollment.enroll(issued.reveal(), csr, "db1") })
        assertEquals(0, countAgents())
        assertEquals(EnrollmentTokenState.REVOKED, tokens.get(acme, issued.id)?.state)
    }

    @Test
    fun `Отозванный токен после срока отклоняется как TOKEN_REVOKED`() {
        val issued = newToken(Duration.ofMinutes(5))
        tokens.revoke(acme, issued.id, clock.now)
        clock.now = ENROLLMENT_NOW + Duration.ofDays(1)
        assertEquals(Reason.TOKEN_REVOKED, rejectionReason { enrollment.enroll(issued.reveal(), csr, "db1") })
    }

    @Test
    fun `Использованный токен после срока отклоняется как TOKEN_USED`() {
        val issued = newToken(Duration.ofMinutes(5))
        enrollment.enroll(issued.reveal(), csr, "db1")
        clock.now = ENROLLMENT_NOW + Duration.ofDays(1)
        val secondCsr = resource("agent-p384.csr")
        assertEquals(Reason.TOKEN_USED, rejectionReason { enrollment.enroll(issued.reveal(), secondCsr, "db2") })
    }

    @Test
    fun `Отказ по токену важнее невалидного CSR`() {
        val issued = newToken()
        enrollment.enroll(issued.reveal(), csr, "db1")
        val garbage = "garbage".toByteArray()
        assertEquals(Reason.TOKEN_USED, rejectionReason { enrollment.enroll(issued.reveal(), garbage, "db2") })
    }

    // --- Неудачная попытка не расходует токен

    @Test
    fun `Невалидный CSR отклоняется как CSR_INVALID и не расходует токен`() {
        val cases =
            mapOf(
                "empty" to ByteArray(0),
                "not-der" to "garbage".toByteArray(),
                "bad-signature" to resource("agent-bad-signature.csr"),
                "rsa" to resource("agent-rsa.csr"),
                "p521" to resource("agent-p521.csr"),
                "ed25519" to resource("agent-ed25519.csr"),
            )
        for ((name, bytes) in cases) {
            val issued = newToken()
            assertEquals(Reason.CSR_INVALID, rejectionReason { enrollment.enroll(issued.reveal(), bytes, "db1") }, name)
        }
        assertEquals(0, countAgents())
    }

    @Test
    fun `Ключ P-384 принимается`() {
        val issued = newToken()
        enrollment.enroll(issued.reveal(), resource("agent-p384.csr"), "db1")
        assertEquals(1, countAgents())
    }

    @Test
    fun `После отказа CSR_INVALID тем же токеном можно зарегистрироваться`() {
        val issued = newToken()
        rejectionReason { enrollment.enroll(issued.reveal(), "garbage".toByteArray(), "db1") }
        enrollment.enroll(issued.reveal(), csr, "db1")
        assertEquals(1, countAgents())
    }

    // --- Отозвать можно любой ещё не использованный токен, отказ всегда с причиной (grpc leg)

    @Test
    fun `Регистрация после отзыва получает TOKEN_REVOKED`() {
        val issued = newToken()
        tokens.revoke(acme, issued.id, clock.now)
        val service = EnrollmentGrpcService(enrollment, Dispatchers.Unconfined)
        val request = enrollRequest(issued.reveal(), csr)
        val error = assertFailsWith<StatusRuntimeException> { runBlocking { service.enroll(request) } }
        assertEquals(Status.Code.UNAUTHENTICATED, Status.fromThrowable(error).code)
        val details = StatusProto.fromThrowable(error)?.detailsList.orEmpty()
        val info = details.single { it.`is`(ErrorInfo::class.java) }.unpack(ErrorInfo::class.java)
        assertEquals("TOKEN_REVOKED", info.reason)
        assertEquals(0, countAgents())
    }
}
