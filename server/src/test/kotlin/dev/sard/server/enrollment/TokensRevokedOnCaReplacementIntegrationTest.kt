// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.CaFingerprint
import dev.sard.server.pki.MovableClock
import dev.sard.server.selfagent.captureEvents
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val NOW: Instant = Instant.parse("2026-10-09T12:00:00Z")
private val DEFAULT_TENANT = UUID.fromString("00000000-0000-0000-0000-000000000001")
private val TENANT_B = UUID.fromString("00000000-0000-0000-0000-0000000000b3")
private val G = CaFingerprint("a".repeat(64))
private val F = CaFingerprint("b".repeat(64))

/** Rule "Сгенерированный CA заменяется импортом…", scenario "Замена CA отзывает активные токены" (OQ-191, Р11). */
@SpringBootTest(properties = ["spring.grpc.server.port=0", "server.port=0"])
@Import(TestcontainersConfiguration::class)
class TokensRevokedOnCaReplacementIntegrationTest(
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val listener = TokensRevokedOnCaReplacement(jdbc, MovableClock(NOW))
    private var counter = 0

    @AfterTest
    fun `forget the tokens`() {
        jdbc.update("delete from enrollment_tokens where tenant_id = ?", TENANT_B)
        jdbc.update("delete from enrollment_tokens where created_at = ?", Timestamp.from(NOW.minusSeconds(3600)))
        jdbc.update("delete from tenants where id = ?", TENANT_B)
    }

    private fun token(
        tenant: UUID,
        expires: Instant = NOW.plusSeconds(3600),
        used: Boolean = false,
        revoked: Boolean = false,
        builtin: Boolean = false,
    ): UUID {
        val id = UUID.randomUUID()
        val hash = ByteArray(32) { (counter + it).toByte() }.also { counter++ }
        if (used) {
            // A used token names an agent, which exists for it.
            jdbc.update(
                "insert into agents (id, tenant_id, hostname, registered_at) values (?, ?, 'h', ?)",
                id,
                tenant,
                Timestamp.from(NOW),
            )
        }
        jdbc.update(
            "insert into enrollment_tokens (id, tenant_id, token_hash, expires_at, used_at, agent_id, created_at, " +
                "revoked_at, builtin, label) values (?, ?, ?, ?, ?, ?, ?, ?, ?, '')",
            id,
            tenant,
            hash,
            Timestamp.from(expires),
            if (used) Timestamp.from(NOW) else null,
            if (used) id else null,
            Timestamp.from(NOW.minusSeconds(3600)),
            if (revoked) Timestamp.from(NOW.minusSeconds(60)) else null,
            builtin,
        )
        return id
    }

    private fun revokedAt(id: UUID): Instant? {
        val sql = "select revoked_at from enrollment_tokens where id = ?"
        return jdbc.queryForObject(sql, Timestamp::class.java, id)?.toInstant()
    }

    @Test
    fun `Активные обычные токены всех тенантов отзываются, одна строка WARN с числом`() {
        jdbc.update("insert into tenants (id, name) values (?, 'B')", TENANT_B)
        val t1 = token(DEFAULT_TENANT)
        val t2 = token(TENANT_B)

        val events = captureEvents { listener.replaced(G, F) }

        assertEquals(NOW, revokedAt(t1))
        assertEquals(NOW, revokedAt(t2))
        val warnings = events.filter { it.level == ch.qos.logback.classic.Level.WARN }
        assertEquals(1, warnings.size, events.toString())
        assertTrue("2" in warnings.single().text && G.hex in warnings.single().text, warnings.single().text)
    }

    @Test
    fun `Использованные, отозванные, истёкшие и встроенные токены не затрагиваются`() {
        val used = token(DEFAULT_TENANT, used = true)
        val revoked = token(DEFAULT_TENANT, revoked = true)
        val expired = token(DEFAULT_TENANT, expires = NOW)
        val builtin = token(DEFAULT_TENANT, builtin = true)

        val events = captureEvents { listener.replaced(G, F) }

        assertEquals(null, revokedAt(used))
        assertEquals(NOW.minusSeconds(60), revokedAt(revoked))
        assertEquals(null, revokedAt(expired))
        assertEquals(null, revokedAt(builtin))
        assertTrue(events.none { it.level == ch.qos.logback.classic.Level.WARN }, events.toString())
        jdbc.update("delete from enrollment_tokens where id in (?, ?, ?, ?)", used, revoked, expired, builtin)
        jdbc.update("delete from agents where id = ?", used)
    }
}
