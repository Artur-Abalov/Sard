// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.context.annotation.Primary
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
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val NOW: Instant = Instant.parse("2026-10-01T12:00:00Z")

@TestConfiguration(proxyBeanMethods = false)
class TokenServiceTestConfiguration {
    @Bean
    fun clock() = MovableClock(NOW)

    @Bean
    @Primary
    fun testAgentEndpoint() = AgentEndpoint("sard.example.com:9090")
}

/**
 * Rules "Строка токена выдаётся один раз", "Срок жизни токена", "Подпись токена", "Состояние
 * токена вычисляется при чтении", "Токены одного тенанта не видны администратору другого" and
 * "Отозвать можно любой ещё не использованный токен".
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, TokenServiceTestConfiguration::class)
class EnrollmentTokenServiceIntegrationTest(
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val ca: CertificateAuthority,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenantA = UUID.randomUUID()
    private val tenantB = UUID.randomUUID()

    @BeforeTest
    fun `two tenants, a known clock`() {
        clock.now = NOW
        val sql = "insert into tenants (id, name) values (?, ?), (?, ?)"
        jdbc.update(sql, tenantA, "a-$tenantA", tenantB, "b-$tenantB")
    }

    @AfterTest
    fun `drop everything of both tenants`() {
        for (tenant in listOf(tenantA, tenantB)) {
            jdbc.update("delete from enrollment_tokens where tenant_id = ?", tenant)
            jdbc.update("delete from tenants where id = ?", tenant)
        }
    }

    // --- Строка токена выдаётся один раз

    @Test
    fun `Создание токена возвращает идентификатор, срок и строку токена`() {
        val issued = tokens.create(tenantA, Duration.ofHours(1))
        assertEquals(NOW + Duration.ofHours(1), issued.expiresAt)
        val parsed = EnrollmentToken.parse(issued.reveal())
        assertEquals(ca.fingerprint(), parsed.fingerprint)
    }

    @Test
    fun `Результат создания токена содержит команду регистрации с настроенным адресом`() {
        val issued = tokens.create(tenantA)
        assertEquals(
            "sudo -u sard-agent sard-agent enroll --server sard.example.com:9090 --token ${issued.reveal()}",
            issued.command(),
        )
    }

    @Test
    fun `Созданный токен активен`() {
        val issued = tokens.create(tenantA, Duration.ofHours(1))
        assertEquals(EnrollmentTokenState.ACTIVE, tokens.get(tenantA, issued.id)?.state)
    }

    @Test
    fun `Сервер хранит только хэш секрета токена`() {
        val issued = tokens.create(tenantA)
        val parsed = EnrollmentToken.parse(issued.reveal())
        val row = jdbc.queryForMap("select token_hash from enrollment_tokens where id = ?", issued.id)
        assertEquals(
            java.util.HexFormat
                .of()
                .formatHex(parsed.secret.hash()),
            java.util.HexFormat
                .of()
                .formatHex(row["token_hash"] as ByteArray),
        )
        val secret = issued.reveal().substringAfter("sard_").substringBefore('.')
        val sql = "select t::text from enrollment_tokens t where id = ?"
        val text = jdbc.queryForObject(sql, String::class.java, issued.id)
        assertFalse(issued.reveal() in text!!, text)
        assertFalse(secret in text, text)
    }

    @Test
    fun `Список токенов не содержит строку токена`() {
        val issued = tokens.create(tenantA)
        val secret = issued.reveal().substringAfter("sard_").substringBefore('.')
        val summary = tokens.list(tenantA).single()
        val text = summary.toString()
        assertFalse(issued.reveal() in text, text)
        assertFalse(secret in text, text)
    }

    @Test
    fun `Карточка токена не содержит строку токена`() {
        val issued = tokens.create(tenantA)
        val secret = issued.reveal().substringAfter("sard_").substringBefore('.')
        val text = checkNotNull(tokens.get(tenantA, issued.id)).toString()
        assertFalse(issued.reveal() in text, text)
        assertFalse(secret in text, text)
    }

    @Test
    fun `Два созданных токена имеют разные секреты`() {
        val first = tokens.create(tenantA)
        val second = tokens.create(tenantA)
        assertTrue(first.reveal() != second.reveal())
        assertTrue(first.id != second.id)
    }

    // --- Срок жизни токена

    @Test
    fun `Токен без указанного срока живёт 24 часа`() {
        val issued = tokens.create(tenantA)
        assertEquals(NOW + Duration.ofHours(24), issued.expiresAt)
    }

    @Test
    fun `Срок на границе диапазона принимается`() {
        assertEquals(NOW + Duration.ofMinutes(5), tokens.create(tenantA, Duration.ofMinutes(5)).expiresAt)
        assertEquals(NOW + Duration.ofDays(7), tokens.create(tenantA, Duration.ofDays(7)).expiresAt)
    }

    @Test
    fun `Срок вне диапазона отклоняется ошибкой валидации`() {
        val ttls =
            listOf(
                Duration.ZERO,
                Duration.ofMinutes(-1),
                Duration.ofMinutes(4).plusSeconds(59),
                Duration.ofDays(7).plusSeconds(1),
            )
        for (ttl in ttls) {
            assertFailsWith<EnrollmentTokenValidationException>(ttl.toString()) { tokens.create(tenantA, ttl) }
        }
        assertEquals(0, countTokens(tenantA))
    }

    // --- Подпись токена

    @Test
    fun `Подпись токена показывается в списке и карточке`() {
        val issued = tokens.create(tenantA, label = "db1 — бухгалтерия")
        assertEquals("db1 — бухгалтерия", tokens.get(tenantA, issued.id)?.label)
        assertEquals("db1 — бухгалтерия", tokens.list(tenantA).single().label)
    }

    @Test
    fun `Токен без подписи создаётся`() {
        val issued = tokens.create(tenantA)
        assertEquals("", tokens.get(tenantA, issued.id)?.label)
    }

    @Test
    fun `Пустая подпись означает отсутствие подписи`() {
        val issued = tokens.create(tenantA, label = "")
        assertEquals("", tokens.get(tenantA, issued.id)?.label)
    }

    @Test
    fun `Подпись из 200 символов принимается`() {
        val label = "x".repeat(200)
        val issued = tokens.create(tenantA, label = label)
        assertEquals(label, tokens.get(tenantA, issued.id)?.label)
    }

    @Test
    fun `Подпись длиннее 200 символов отклоняется`() {
        assertFailsWith<EnrollmentTokenValidationException> { tokens.create(tenantA, label = "x".repeat(201)) }
        assertEquals(0, countTokens(tenantA))
    }

    // --- Состояние токена вычисляется при чтении

    @Test
    fun `Список показывает состояние каждого токена`() {
        val active = tokens.create(tenantA, Duration.ofHours(1))
        val used = tokens.create(tenantA, Duration.ofHours(1))
        markUsed(used.id)
        val expired = tokens.create(tenantA, Duration.ofMinutes(5))
        val revoked = tokens.create(tenantA, Duration.ofHours(1))
        tokens.revoke(tenantA, revoked.id, clock.now)
        clock.now = NOW + Duration.ofMinutes(5)

        val states = tokens.list(tenantA).associate { it.id to it.state }
        assertEquals(EnrollmentTokenState.ACTIVE, states[active.id])
        assertEquals(EnrollmentTokenState.USED, states[used.id])
        assertEquals(EnrollmentTokenState.EXPIRED, states[expired.id])
        assertEquals(EnrollmentTokenState.REVOKED, states[revoked.id])
    }

    @Test
    fun `Список упорядочен по времени создания, новые первыми`() {
        val x = tokens.create(tenantA)
        clock.now = NOW + Duration.ofHours(1)
        val y = tokens.create(tenantA)
        clock.now = NOW + Duration.ofHours(1) + Duration.ofMinutes(30)
        val z = tokens.create(tenantA)

        assertEquals(listOf(z.id, y.id, x.id), tokens.list(tenantA).map { it.id })
    }

    @Test
    fun `Завершённые токены остаются в списке`() {
        val used = tokens.create(tenantA, Duration.ofHours(1))
        markUsed(used.id)
        val expired = tokens.create(tenantA, Duration.ofMinutes(5))
        val revoked = tokens.create(tenantA, Duration.ofHours(1))
        tokens.revoke(tenantA, revoked.id, clock.now)
        clock.now = NOW + Duration.ofDays(365)

        assertEquals(setOf(used.id, expired.id, revoked.id), tokens.list(tenantA).map { it.id }.toSet())
    }

    @Test
    fun `Использованный токен после срока остаётся использованным`() {
        val issued = tokens.create(tenantA, Duration.ofHours(1))
        markUsed(issued.id)
        clock.now = NOW + Duration.ofDays(1)
        assertEquals(EnrollmentTokenState.USED, tokens.get(tenantA, issued.id)?.state)
    }

    @Test
    fun `Отозванный токен после срока остаётся отозванным`() {
        val issued = tokens.create(tenantA, Duration.ofHours(1))
        tokens.revoke(tenantA, issued.id, clock.now)
        clock.now = NOW + Duration.ofDays(1)
        assertEquals(EnrollmentTokenState.REVOKED, tokens.get(tenantA, issued.id)?.state)
    }

    @Test
    fun `Карточка использованного токена называет зарегистрированного агента`() {
        val issued = tokens.create(tenantA, Duration.ofHours(1))
        val agentId = UUID.randomUUID()
        jdbc.update(
            "insert into agents (id, tenant_id, hostname, registered_at) values (?, ?, ?, ?)",
            agentId,
            tenantA,
            "db1",
            java.sql.Timestamp.from(clock.now),
        )
        jdbc.update(
            "update enrollment_tokens set used_at = ?, agent_id = ? where id = ?",
            java.sql.Timestamp.from(clock.now),
            agentId,
            issued.id,
        )

        val card = tokens.get(tenantA, issued.id)
        assertEquals(EnrollmentTokenState.USED, card?.state)
        assertEquals(agentId, card?.agentId)
        assertEquals(clock.now, card?.usedAt)
        jdbc.update("delete from agent_certificates where agent_id = ?", agentId)
        jdbc.update("delete from enrollment_tokens where agent_id = ?", agentId)
        jdbc.update("delete from agents where id = ?", agentId)
    }

    // --- Токены одного тенанта не видны администратору другого

    @Test
    fun `Список токенов тенанта не содержит токенов другого тенанта`() {
        tokens.create(tenantA)
        assertEquals(emptyList(), tokens.list(tenantB))
    }

    @Test
    fun `Карточка токена другого тенанта не находится`() {
        val issued = tokens.create(tenantA)
        assertNull(tokens.get(tenantB, issued.id))
    }

    // --- Отозвать можно любой ещё не использованный токен, отказ всегда с причиной

    @Test
    fun `Администратор отзывает активный токен`() {
        val issued = tokens.create(tenantA, Duration.ofHours(1))
        val result = tokens.revoke(tenantA, issued.id, clock.now)
        assertEquals(RevokeResult.Revoked(NOW), result)
        val card = tokens.get(tenantA, issued.id)
        assertEquals(EnrollmentTokenState.REVOKED, card?.state)
        assertEquals(NOW, card?.revokedAt)
    }

    @Test
    fun `Истёкший токен отозвать нельзя`() {
        val issued = tokens.create(tenantA, Duration.ofMinutes(5))
        clock.now = NOW + Duration.ofMinutes(10)
        val result = tokens.revoke(tenantA, issued.id, clock.now)
        assertEquals(RevokeResult.Rejected(RevokeRejection.EXPIRED), result)
        val card = tokens.get(tenantA, issued.id)
        assertEquals(EnrollmentTokenState.EXPIRED, card?.state)
        assertNull(card?.revokedAt)
    }

    @Test
    fun `Использованный токен отозвать нельзя`() {
        val issued = tokens.create(tenantA, Duration.ofHours(1))
        markUsed(issued.id)
        assertEquals(RevokeResult.Rejected(RevokeRejection.USED), tokens.revoke(tenantA, issued.id, clock.now))
        assertEquals(EnrollmentTokenState.USED, tokens.get(tenantA, issued.id)?.state)
    }

    @Test
    fun `Повторный отзыв токена ничего не меняет`() {
        val issued = tokens.create(tenantA, Duration.ofHours(1))
        tokens.revoke(tenantA, issued.id, NOW)
        clock.now = NOW + Duration.ofMinutes(10)
        val result = tokens.revoke(tenantA, issued.id, clock.now)
        assertEquals(RevokeResult.Revoked(NOW), result)
        assertEquals(NOW, tokens.get(tenantA, issued.id)?.revokedAt)
    }

    @Test
    fun `Отзыв токена другого тенанта не находит его`() {
        val issued = tokens.create(tenantA, Duration.ofHours(1))
        assertEquals(RevokeResult.NotFound, tokens.revoke(tenantB, issued.id, clock.now))
        assertEquals(EnrollmentTokenState.ACTIVE, tokens.get(tenantA, issued.id)?.state)
    }

    @Test
    fun `Отзыв несуществующего токена не находит его`() {
        assertEquals(RevokeResult.NotFound, tokens.revoke(tenantA, UUID.randomUUID(), clock.now))
    }

    private fun countTokens(tenant: UUID): Int? =
        jdbc.queryForObject("select count(*) from enrollment_tokens where tenant_id = ?", Int::class.java, tenant)

    private fun markUsed(id: UUID) {
        jdbc.update("update enrollment_tokens set used_at = ? where id = ?", java.sql.Timestamp.from(clock.now), id)
    }
}
