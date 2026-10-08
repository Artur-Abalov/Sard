// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.PkiFixtures.resource
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
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

private val T0: Instant = Instant.parse("2026-10-01T12:00:00Z")
private val MARGIN: Duration = Duration.ofMinutes(10)

/**
 * Rules "Встроенный токен не виден и не управляется через API" (service level),
 * "Регистрация по встроенному токену подчиняется обычным правилам токенов" and
 * "Проверка выпускает встроенный токен..." (the token side) of docs/specs/server/self-agent.feature.
 */
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

    private fun hashOf(token: String) = EnrollmentToken.parse(token).secret.hash()

    private fun usable(token: String) = tokens.builtinUsable(tenant, hashOf(token), MARGIN)

    private fun builtinAgents() =
        jdbc.queryForObject("select count(*) from agents where tenant_id = ? and builtin", Int::class.java, tenant)

    private fun revokedAt(id: UUID): Instant? =
        jdbc
            .queryForObject("select revoked_at from enrollment_tokens where id = ?", java.sql.Timestamp::class.java, id)
            ?.toInstant()

    @Test
    fun `Встроенный токен живёт ровно час и помечен в базе`() {
        val issued = tokens.replaceBuiltin(tenant)
        assertEquals(T0 + Duration.ofHours(1), issued.expiresAt)
        assertEquals(
            true,
            jdbc.queryForObject("select builtin from enrollment_tokens where id = ?", Boolean::class.java, issued.id),
        )
    }

    @Test
    fun `Встроенный токен не виден в списке, карточке и отзыве`() {
        val builtin = tokens.replaceBuiltin(tenant)
        val ordinary = tokens.create(tenant)

        assertEquals(listOf(ordinary.id), tokens.list(tenant).map { it.id })
        assertEquals(null, tokens.get(tenant, builtin.id))
        assertEquals(RevokeResult.NotFound, tokens.revoke(tenant, builtin.id, T0))
        assertEquals(null, revokedAt(builtin.id))
    }

    @Test
    fun `Обычный токен после отзыва остаётся обычным`() {
        val ordinary = tokens.create(tenant)
        assertEquals(RevokeResult.Revoked(T0), tokens.revoke(tenant, ordinary.id, T0))
    }

    @Test
    fun `Пригоден токен, чей хэш совпал, срок которого строго позже запаса`() {
        val issued = tokens.replaceBuiltin(tenant)
        assertTrue(usable(issued.reveal()))
        clock.now = T0 + Duration.ofMinutes(50) - Duration.ofMillis(1)
        assertTrue(usable(issued.reveal()))
        clock.now = T0 + Duration.ofMinutes(50)
        assertFalse(usable(issued.reveal()))
    }

    @Test
    fun `Чужой хэш, обычный токен и использованный токен не пригодны`() {
        val builtin = tokens.replaceBuiltin(tenant)
        val ordinary = tokens.create(tenant)
        val other = UUID.randomUUID()
        assertFalse(tokens.builtinUsable(tenant, ByteArray(32), MARGIN))
        assertFalse(usable(ordinary.reveal()))
        assertFalse(tokens.builtinUsable(other, hashOf(builtin.reveal()), MARGIN))
        enrollment.enroll(builtin.reveal(), csr, "sard-self")
        assertFalse(usable(builtin.reveal()))
    }

    @Test
    fun `Выпуск нового встроенного токена отзывает прочие активные встроенные, но не обычные`() {
        val first = tokens.replaceBuiltin(tenant)
        val second = tokens.replaceBuiltin(tenant)
        val ordinary = tokens.create(tenant)
        val expired = tokens.replaceBuiltin(tenant)
        clock.now = T0 + Duration.ofHours(2)
        val third = tokens.replaceBuiltin(tenant)

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
        enrollment.enroll(tokens.replaceBuiltin(tenant).reveal(), csr, "sard-self")
        assertEquals(1, builtinAgents())
        enrollment.enroll(tokens.create(tenant).reveal(), csr, "sard-self")
        assertEquals(1, builtinAgents())
        assertEquals(2, jdbc.countAgents(tenant))
    }

    @Test
    fun `Второй неотозванный встроенный агент не создаётся, токен остаётся активным`() {
        enrollment.enroll(tokens.replaceBuiltin(tenant).reveal(), csr, "sard-self")
        val second = tokens.replaceBuiltin(tenant)

        assertEquals(
            EnrollmentRejectedException.Reason.INTERNAL_RETRYABLE,
            rejectionReason { enrollment.enroll(second.reveal(), csr, "sard-self") },
        )

        assertEquals(1, jdbc.countAgents(tenant))
        assertTrue(usable(second.reveal()))
    }

    @Test
    fun `После отзыва встроенного агента новый встроенный регистрируется`() {
        enrollment.enroll(tokens.replaceBuiltin(tenant).reveal(), csr, "sard-self")
        jdbc.update("update agents set revoked_at = ? where tenant_id = ?", java.sql.Timestamp.from(T0), tenant)

        enrollment.enroll(tokens.replaceBuiltin(tenant).reveal(), csr, "sard-self")

        assertEquals(2, builtinAgents())
    }
}
