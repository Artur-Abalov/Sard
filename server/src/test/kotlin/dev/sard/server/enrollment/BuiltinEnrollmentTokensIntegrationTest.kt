// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.PkiFixtures.resource
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

private val T0: Instant = Instant.parse("2026-10-01T12:00:00Z")
private const val WRITE = "update agents set hostname = hostname where false"
private val MARGIN: Duration = Duration.ofMinutes(10)

/**
 * Rules "Встроенный токен не виден и не управляется через API" (service level),
 * "Регистрация по встроенному токену подчиняется обычным правилам токенов" and
 * "Проверка выпускает встроенный токен..." (the token side) of docs/specs/server/self-agent.feature.
 */
@MutFlowTest
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, TokenServiceTestConfiguration::class)
class BuiltinEnrollmentTokensIntegrationTest(
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val enrollment: Enrollment,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val sessions: TenantSessions,
) {
    private val tenant = UUID.randomUUID()
    private val csr = resource("agent-p256.csr")

    @BeforeTest
    fun `a fresh tenant, a known clock`() {
        clock.now = T0
        jdbc.insertTenant(tenant)
    }

    @AfterTest
    fun `drop the tenant`() = jdbc.deleteEnrollmentTenantData(tenant)

    private fun replaceBuiltin() = MutFlow.underTest { tokens.replaceBuiltin(tenant) }

    private fun list() = MutFlow.underTest { tokens.list(tenant) }

    private fun get(id: UUID) = MutFlow.underTest { tokens.get(tenant, id) }

    private fun revoke(
        id: UUID,
        at: Instant,
    ) = MutFlow.underTest { tokens.revoke(tenant, id, at) }

    private fun builtinUsable(
        owner: UUID,
        hash: ByteArray,
        margin: Duration,
    ) = MutFlow.underTest { tokens.builtinUsable(owner, hash, margin) }

    private fun enroll(
        token: String,
        csr: ByteArray,
        hostname: String,
    ) = MutFlow.underTest { enrollment.enroll(token, csr, hostname) }

    private fun hashOf(token: String) = EnrollmentToken.parse(token).secret.hash()

    private fun usable(token: String) = builtinUsable(tenant, hashOf(token), MARGIN)

    private fun builtinAgents() =
        jdbc.queryForObject("select count(*) from agents where tenant_id = ? and builtin", Int::class.java, tenant)

    private fun revokedAt(id: UUID): Instant? =
        jdbc
            .queryForObject("select revoked_at from enrollment_tokens where id = ?", java.sql.Timestamp::class.java, id)
            ?.toInstant()

    @Test
    fun `Встроенный токен живёт ровно час и помечен в базе`() {
        val issued = replaceBuiltin()
        assertEquals(T0 + Duration.ofHours(1), issued.expiresAt)
        assertEquals(
            true,
            jdbc.queryForObject("select builtin from enrollment_tokens where id = ?", Boolean::class.java, issued.id),
        )
    }

    @Test
    fun `Встроенный токен не виден в списке, карточке и отзыве`() {
        val builtin = replaceBuiltin()
        val ordinary = tokens.create(tenant)

        assertEquals(listOf(ordinary.id), list().map { it.id })
        assertEquals(EnrollmentTokenState.ACTIVE, list().single().state)
        assertEquals(null, get(builtin.id))
        assertEquals(RevokeResult.NotFound, revoke(builtin.id, T0))
        assertEquals(null, revokedAt(builtin.id))
    }

    @Test
    fun `Обычный токен после отзыва остаётся обычным`() {
        val ordinary = tokens.create(tenant)
        assertEquals(RevokeResult.Revoked(T0), revoke(ordinary.id, T0))
    }

    @Test
    fun `Пригоден токен, чей хэш совпал, срок которого строго позже запаса`() {
        val issued = replaceBuiltin()
        assertTrue(usable(issued.reveal()))
        clock.now = T0 + Duration.ofMinutes(50) - Duration.ofMillis(1)
        assertTrue(usable(issued.reveal()))
        clock.now = T0 + Duration.ofMinutes(50)
        assertFalse(usable(issued.reveal()))
    }

    @Test
    fun `Чужой хэш, обычный токен и использованный токен не пригодны`() {
        val builtin = replaceBuiltin()
        val ordinary = tokens.create(tenant)
        val other = UUID.randomUUID()
        assertFalse(builtinUsable(tenant, ByteArray(32), MARGIN))
        assertFalse(usable(ordinary.reveal()))
        assertFalse(builtinUsable(other, hashOf(builtin.reveal()), MARGIN))
        enroll(builtin.reveal(), csr, "sard-self")
        assertFalse(usable(builtin.reveal()))
    }

    @Test
    fun `Выпуск нового встроенного токена отзывает прочие активные встроенные, но не обычные`() {
        val first = replaceBuiltin()
        val second = replaceBuiltin()
        val ordinary = tokens.create(tenant)
        val expired = replaceBuiltin()
        clock.now = T0 + Duration.ofHours(2)
        val third = replaceBuiltin()

        assertEquals(T0, revokedAt(first.id))
        assertEquals(T0, revokedAt(second.id))
        assertEquals(null, revokedAt(expired.id))
        assertEquals(null, revokedAt(ordinary.id))
        assertEquals(null, revokedAt(third.id))
        assertNotEquals(first.id, third.id)
        assertFalse(usable(first.reveal()))
        assertTrue(usable(third.reveal()))
    }

    @Test
    fun `Регистрация по встроенному токену создаёт встроенного агента, по обычному - обычного`() {
        enroll(replaceBuiltin().reveal(), csr, "sard-self")
        assertEquals(1, builtinAgents())
        enroll(tokens.create(tenant).reveal(), csr, "sard-self")
        assertEquals(1, builtinAgents())
        assertEquals(2, jdbc.countAgents(tenant))
    }

    @Test
    fun `Второй неотозванный встроенный агент не создаётся, токен остаётся активным`() {
        enroll(replaceBuiltin().reveal(), csr, "sard-self")
        val second = replaceBuiltin()

        assertEquals(
            EnrollmentRejectedException.Reason.INTERNAL_RETRYABLE,
            rejectionReason { enroll(second.reveal(), csr, "sard-self") },
        )

        assertEquals(1, jdbc.countAgents(tenant))
        assertTrue(usable(second.reveal()))
    }

    @Test
    fun `После отзыва встроенного агента новый встроенный регистрируется`() {
        enroll(replaceBuiltin().reveal(), csr, "sard-self")
        jdbc.update("update agents set revoked_at = ? where tenant_id = ?", java.sql.Timestamp.from(T0), tenant)

        enroll(replaceBuiltin().reveal(), csr, "sard-self")

        assertEquals(2, builtinAgents())
    }

    private fun reasonOf(
        token: String,
        csr: ByteArray = this.csr,
        hostname: String = "sard-self",
    ) = rejectionReason { enroll(token, csr, hostname) }

    @Test
    fun `Строка токена, нарушающая формат, отклоняется как TOKEN_MALFORMED`() {
        val token = replaceBuiltin().reveal()
        val secret = token.substringAfter("sard_").substringBefore('.')
        val fingerprint = token.substringAfter('.')
        val broken =
            listOf(
                token.removePrefix("sard_"),
                "sard_$secret$fingerprint",
                "sard_${secret.drop(1)}.$fingerprint",
                "sard_${secret.dropLast(1)}+.$fingerprint",
                "sard_${secret.dropLast(1)}B.$fingerprint",
                "sard_$secret.${fingerprint.drop(1)}",
                "sard_$secret.${fingerprint.drop(1)}G",
            )
        for (case in broken) {
            assertEquals(EnrollmentRejectedException.Reason.TOKEN_MALFORMED, reasonOf(case), case)
        }
        assertTrue(usable(token))
    }

    @Test
    fun `Запрос с ключом не той кривой или без доказательства владения отклоняется, ключ P-384 принимается`() {
        for (name in listOf("agent-p521", "agent-rsa", "agent-ed25519", "agent-bad-signature")) {
            val token = replaceBuiltin().reveal()
            assertEquals(EnrollmentRejectedException.Reason.CSR_INVALID, reasonOf(token, resource("$name.csr")), name)
            assertTrue(usable(token), name)
        }
        enroll(replaceBuiltin().reveal(), resource("agent-p384.csr"), "sard-self")
        assertEquals(1, builtinAgents())
    }

    @Test
    fun `Имя хоста длиннее 253 символов, пустое или с управляющим символом отклоняется`() {
        for (hostname in listOf("", "a".repeat(254), "db\u0007", "\u0000")) {
            val token = replaceBuiltin().reveal()
            assertEquals(EnrollmentRejectedException.Reason.HOSTNAME_INVALID, reasonOf(token, hostname = hostname))
            assertTrue(usable(token), hostname)
        }
        enroll(replaceBuiltin().reveal(), csr, "a".repeat(253))
        assertEquals(1, builtinAgents())
    }

    @Test
    fun `Сессия системы только читает`() {
        assertFailsWith<Exception> {
            MutFlow.underTest {
                sessions.system { session ->
                    session.doWork { connection: java.sql.Connection ->
                        connection.createStatement().use { it.execute(WRITE) }
                    }
                }
            }
        }
    }
}
