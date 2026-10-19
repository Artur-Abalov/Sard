// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.agents.stream.TestAgent
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val DAY: Duration = Duration.ofDays(1)

/**
 * C1 phase 3, rules "Список и карточка агента показывают срок текущего сертификата", "Текущий сертификат —
 * последний подтверждённый и не отозванный" and "REST отдаёт только то, что сервер знает сам"
 * (docs/specs/server/agent-certificate-expiry.feature). The clock is the server's Clock bean, replaced by the
 * test's [MovableClock] (П42); records of renewals are written the way Enroll and RenewCertificate write them.
 */
@RestApiTest
class AgentCertificateApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired private val mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val clock = world.clock
    private val tenant = world.tenant()
    private var admin = world.admin(tenant)

    /**
     * Moves the server's clock. A session of the console ends with the clock, so the administrator signs in again
     * (the session lives on the server's time, like the certificate).
     */
    private fun moveTo(moment: Instant) {
        clock.now = moment
        admin = world.admin(tenant)
    }

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun card(agent: TestAgent): JsonNode = world.api.get("/api/v1/agents/${agent.agentId}", admin).json

    private fun listed(agent: TestAgent): JsonNode =
        world.api
            .get("/api/v1/agents?limit=200", admin)
            .json
            .path("items")
            .list()
            .first { it.path("id").asString() == agent.agentId.toString() }

    private fun certificate(agent: TestAgent): JsonNode = card(agent).path("certificate")

    /** The serial of the only certificate Enroll issued to [agent]. */
    private fun serialOf(agent: TestAgent): String =
        world.jdbc.queryForObject(
            "select serial from agent_certificates where agent_id = ?",
            String::class.java,
            agent.agentId,
        )!!

    /** The certificate of [agent] with the lifetime [validity] counted from its issue (the record, rewritten). */
    private fun lifetime(
        agent: TestAgent,
        validity: Duration,
    ) {
        world.jdbc.update(
            "update agent_certificates set not_after = issued_at + ? * interval '1 second' where agent_id = ?",
            validity.seconds,
            agent.agentId,
        )
    }

    /** A record of a renewal of [predecessor], issued at [issuedAt] for a year, as RenewCertificate writes it. */
    private fun renewed(
        agent: TestAgent,
        predecessor: String,
        issuedAt: Instant,
        supersededAt: Instant? = null,
    ): String {
        val serial = UUID.randomUUID().toString().replace("-", "")
        world.jdbc.update(
            "insert into agent_certificates (serial, tenant_id, agent_id, issued_at, not_after, predecessor_serial, " +
                "superseded_at) values (?, ?, ?, ?, ?, ?, ?)",
            serial,
            tenant,
            agent.agentId,
            Timestamp.from(issuedAt),
            Timestamp.from(issuedAt + Duration.ofDays(365)),
            predecessor,
            supersededAt?.let(Timestamp::from),
        )
        return serial
    }

    /** The confirmation of [serial]: the certificate it was issued against is superseded (C1, П9). */
    private fun confirmed(
        predecessor: String,
        at: Instant,
    ) {
        val sql = "update agent_certificates set superseded_at = ? where serial = ?"
        world.jdbc.update(sql, Timestamp.from(at), predecessor)
    }

    @Test
    fun `Карточка агента отдаёт срок и состояние текущего сертификата`() {
        val agent = world.agent(tenant)
        moveTo(T0 + DAY.multipliedBy(10))

        val json = certificate(agent)

        val fields = setOf("notBefore", "notAfter", "daysLeft", "state", "renewalIssuedAt")
        assertEquals(fields, json.propertyNames().toSet())
        assertEquals(T0.toString(), json.path("notBefore").asString())
        assertEquals((T0 + DAY.multipliedBy(365)).toString(), json.path("notAfter").asString())
        assertEquals(355, json.path("daysLeft").asInt())
        assertEquals("ok", json.path("state").asString())
        assertTrue(json.path("renewalIssuedAt").isNull)
    }

    @Test
    fun `Список агентов отдаёт тот же сертификат, что и карточка`() {
        val agent = world.agent(tenant)
        moveTo(T0 + DAY.multipliedBy(10))

        assertEquals(certificate(agent), listed(agent).path("certificate"))
    }

    @Test
    fun `Состояние сертификата следует за долями срока`() {
        val agent = world.agent(tenant)
        val cases =
            listOf(
                Triple(DAY.multipliedBy(243).plusHours(8).minusSeconds(1), "ok", 121),
                Triple(DAY.multipliedBy(243).plusHours(8), "renewal_due", 121),
                Triple(DAY.multipliedBy(335).minusSeconds(1), "renewal_due", 30),
                Triple(DAY.multipliedBy(335), "expiring", 30),
                Triple(DAY.multipliedBy(365).minusSeconds(1), "expiring", 0),
                Triple(DAY.multipliedBy(365), "expired", 0),
                Triple(DAY.multipliedBy(400), "expired", 0),
            )
        for ((elapsed, state, days) in cases) {
            moveTo(T0 + elapsed)
            val json = certificate(agent)
            assertEquals(state, json.path("state").asString(), "state at $elapsed")
            assertEquals(days, json.path("daysLeft").asInt(), "days at $elapsed")
        }
    }

    @Test
    fun `Состояние сертификата на десять минут следует за долями срока`() {
        val agent = world.agent(tenant)
        lifetime(agent, Duration.ofMinutes(10))
        val cases =
            listOf(
                399L to "ok",
                400L to "renewal_due",
                550L to "renewal_due",
                551L to "expiring",
                600L to "expired",
            )
        for ((seconds, state) in cases) {
            moveTo(T0 + Duration.ofSeconds(seconds))
            assertEquals(state, certificate(agent).path("state").asString(), "state at $seconds s")
        }
    }

    @Test
    fun `Пороги считаются от собственного срока сертификата, а не от настройки`() {
        val agent = world.agent(tenant)
        moveTo(T0 + DAY.multipliedBy(300))

        assertEquals("renewal_due", certificate(agent).path("state").asString())
    }

    @Test
    fun `Обрезанный по CA сертификат судится по своему сроку`() {
        val agent = world.agent(tenant)
        lifetime(agent, DAY.multipliedBy(30))
        moveTo(T0 + DAY.multipliedBy(27).plusHours(15))

        val json = certificate(agent)

        assertEquals("expiring", json.path("state").asString())
        assertEquals((T0 + DAY.multipliedBy(30)).toString(), json.path("notAfter").asString())
    }

    @Test
    fun `Выданный, но не подтверждённый сертификат виден как ожидающий подтверждения`() {
        val agent = world.agent(tenant)
        val issuedAt = T0 + DAY.multipliedBy(250)
        renewed(agent, serialOf(agent), issuedAt)
        moveTo(issuedAt)

        val json = certificate(agent)

        assertEquals((T0 + DAY.multipliedBy(365)).toString(), json.path("notAfter").asString())
        assertEquals("renewal_due", json.path("state").asString())
        assertEquals(issuedAt.toString(), json.path("renewalIssuedAt").asString())
    }

    @Test
    fun `После подтверждения карточка показывает новый сертификат`() {
        val agent = world.agent(tenant)
        val first = serialOf(agent)
        val issuedAt = T0 + DAY.multipliedBy(335)
        renewed(agent, first, issuedAt)
        confirmed(first, issuedAt)
        moveTo(issuedAt)

        val json = certificate(agent)

        assertEquals(issuedAt.toString(), json.path("notBefore").asString())
        assertEquals((issuedAt + DAY.multipliedBy(365)).toString(), json.path("notAfter").asString())
        assertEquals("ok", json.path("state").asString())
        assertTrue(json.path("renewalIssuedAt").isNull)
    }

    @Test
    fun `Неподтверждённый сертификат, заменённый следующим продлением, не показывается`() {
        val agent = world.agent(tenant)
        val first = serialOf(agent)
        val second = T0 + DAY.multipliedBy(250)
        val third = second + Duration.ofHours(1)
        renewed(agent, first, second, supersededAt = third)
        renewed(agent, first, third)
        moveTo(third)

        val json = certificate(agent)

        assertEquals((T0 + DAY.multipliedBy(365)).toString(), json.path("notAfter").asString())
        assertEquals(third.toString(), json.path("renewalIssuedAt").asString())
    }

    @Test
    fun `У отозванного агента сертификата нет`() {
        val agent = world.agent(tenant)
        world.api.post("/api/v1/agents/${agent.agentId}/revoke", admin, null)

        assertTrue(card(agent).path("certificate").isNull)
        assertTrue(listed(agent).path("certificate").isNull)
        assertEquals(true, card(agent).has("certificate"))
    }

    @Test
    fun `Отозванная запись сертификата у неотозванного агента не становится текущей`() {
        val agent = world.agent(tenant)
        val sql = "update agent_certificates set revoked_at = ? where agent_id = ?"
        world.jdbc.update(sql, Timestamp.from(T0), agent.agentId)

        assertTrue(certificate(agent).isNull)
    }

    @Test
    fun `При двух подтверждённых записях текущей считается выпущенная позже`() {
        val agent = world.agent(tenant)
        val later = T0 + DAY
        world.jdbc.update(
            "insert into agent_certificates (serial, tenant_id, agent_id, issued_at, not_after) values (?, ?, ?, ?, ?)",
            "f".repeat(32),
            tenant,
            agent.agentId,
            Timestamp.from(later),
            Timestamp.from(later + DAY.multipliedBy(365)),
        )

        assertEquals(later.toString(), certificate(agent).path("notBefore").asString())
    }

    @Test
    fun `Встроенный агент показывает сертификат так же, как обычный`() {
        val agent = world.agent(tenant, hostname = "sard-self", builtin = true)
        moveTo(T0 + DAY.multipliedBy(335))

        assertEquals("expiring", certificate(agent).path("state").asString())
    }

    @Test
    fun `Сертификат агента другого тенанта не виден`() {
        val other = world.tenant()
        val foreign = world.agent(other)
        val mine = world.agent(tenant)

        val ids =
            world.api
                .get("/api/v1/agents?limit=200", admin)
                .json
                .pluck("items", "id")

        assertEquals(listOf(mine.agentId.toString()), ids)
        assertEquals(404, world.api.get("/api/v1/agents/${foreign.agentId}", admin).status)
    }

    @Test
    fun `Отказ в продлении на сервере не меняет состояния сертификата`() {
        val agent = world.agent(tenant)
        moveTo(T0 + DAY.multipliedBy(250))

        val json = certificate(agent)

        assertEquals("renewal_due", json.path("state").asString())
        assertTrue(json.path("renewalIssuedAt").isNull)
    }

    @Test
    fun `Офлайн-агент после двух третей срока остаётся в состоянии renewal_due`() {
        val agent = world.agent(tenant)
        moveTo(T0 + DAY.multipliedBy(300))

        val json = card(agent)

        assertEquals("renewal_due", json.path("certificate").path("state").asString())
        assertEquals("offline", json.path("status").asString())
    }
}
