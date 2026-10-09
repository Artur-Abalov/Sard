// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.agents.stream.Ended
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import io.grpc.Status
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.sql.Timestamp
import java.time.Duration
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** Rules "Агенты — данные последнего Register и онлайн-статус", "Пометка дубликата сессии", "Отзыв агента". */
@RestApiTest
class AgentsApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired private val mapper: ObjectMapper,
    @Autowired private val hellos: ReconciledHellos,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val clock = world.clock
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun card(agent: UUID) = world.api.get("/api/v1/agents/$agent", admin)

    private fun listed(agent: UUID) =
        world.api
            .get("/api/v1/agents?limit=200", admin)
            .json
            .path("items")
            .list()
            .first { it.path("id").asString() == agent.toString() }

    /** The agent opens a stream, says Hello and the server has reconciled it. */
    private fun online(agent: dev.sard.server.agents.stream.TestAgent) =
        world.connect(agent).also {
            it.hello()
            hellos.await(agent.agentId)
        }

    @Test
    fun `Карточка агента содержит имена секретов и скриптов, но не значения`() {
        val agent =
            world.agent(
                tenant,
                snapshotOf(secretNames = listOf("db-password"), scriptNames = listOf("pre-dump")),
            )

        val json = card(agent.agentId).json

        assertEquals(listOf("db-password"), json.path("secretNames").list().map { it.asString() })
        assertEquals(listOf("pre-dump"), json.path("scriptNames").list().map { it.asString() })
        assertEquals(
            setOf(
                "id",
                "hostname",
                "status",
                "agentVersion",
                "os",
                "arch",
                "registeredAt",
                "lastSeenAt",
                "revokedAt",
                "duplicateSessionAt",
                "protocolVersion",
                "plugins",
                "repositories",
                "secretNames",
                "scriptNames",
                "outdated",
                "builtin",
            ),
            json.propertyNames().toSet(),
        )
    }

    @Test
    fun `Карточка агента показывает данные последнего Register`() {
        val agent = world.agent(tenant, snapshotOf(version = "0.4.0"))
        world.register(agent, snapshotOf(version = "0.4.1", repositories = emptyList()))

        val json = card(agent.agentId).json

        assertEquals("0.4.1", json.path("agentVersion").asString())
        assertEquals("linux", json.path("os").asString())
        assertEquals("amd64", json.path("arch").asString())
        assertEquals(1, json.path("protocolVersion").asInt())
        assertEquals(listOf("files"), json.pluck("plugins", "name"))
        assertEquals(mapper.readTree(FILES_SCHEMA), json.path("plugins").get(0).path("configSchema"))
        assertEquals(
            listOf("backup", "restore"),
            json
                .path("plugins")
                .get(0)
                .path("actions")
                .list()
                .map { it.asString() },
        )
        assertTrue(json.path("repositories").list().isEmpty())
    }

    @Test
    fun `Агент без Register показан с пустыми данными снимка`() {
        val agent = world.enroll(tenant)

        val json = card(agent.agentId).json

        for (field in listOf("agentVersion", "os", "arch", "protocolVersion", "lastSeenAt")) {
            assertTrue(json.path(field).isNull, field)
        }
        for (field in listOf("plugins", "repositories", "secretNames", "scriptNames")) {
            assertTrue(json.path(field).isEmpty, field)
        }
        assertEquals("offline", json.path("status").asString())
    }

    @Test
    fun `Нерелизная сборка сервера не помечает никого`() {
        val agent = world.agent(tenant, snapshotOf(version = "v1.3.2"))

        assertFalse(card(agent.agentId).json.path("outdated").asBoolean())
        assertFalse(listed(agent.agentId).path("outdated").asBoolean())
    }

    @Test
    fun `Устаревшая версия агента не мешает его работе`() {
        val agent = world.agent(tenant, snapshotOf(version = "v0.0.1"))
        online(agent)

        assertEquals("online", card(agent.agentId).json.path("status").asString())
    }

    @Test
    fun `Агент с открытым стримом онлайн`() {
        val agent = world.agent(tenant)
        online(agent)

        assertEquals("online", card(agent.agentId).json.path("status").asString())
        assertEquals("online", listed(agent.agentId).path("status").asString())
    }

    @Test
    fun `Агент без открытого стрима офлайн, даже если lastSeenAt только что`() {
        val agent = world.agent(tenant)
        world.jdbc.update(
            "update agents set last_seen_at = ? where id = ?",
            Timestamp.from(T0.minusSeconds(1)),
            agent.agentId,
        )

        val json = card(agent.agentId).json

        assertEquals("offline", json.path("status").asString())
        assertEquals(T0.minusSeconds(1).toString(), json.path("lastSeenAt").asString())
    }

    @Test
    fun `Агент, чей стрим молчит дольше трёх интервалов, офлайн`() {
        val agent = world.agent(tenant)
        online(agent)

        clock.now = T0.plusSeconds(91)

        assertEquals("offline", card(agent.agentId).json.path("status").asString())
    }

    @Test
    fun `Список агентов фильтруется по онлайн-статусу`() {
        val x = world.agent(tenant)
        val y = world.agent(tenant)
        val z = world.agent(tenant)
        online(x)

        fun ids(status: String) =
            world.api
                .get("/api/v1/agents?status=$status", admin)
                .json
                .pluck("items", "id")
                .toSet()

        assertEquals(setOf(x.agentId.toString()), ids("online"))
        assertEquals(setOf(y.agentId.toString(), z.agentId.toString()), ids("offline"))
    }

    @Test
    fun `Карточка несуществующего агента отвечает 404`() {
        val response = card(UUID.randomUUID())

        assertEquals(404, response.status)
        assertEquals("not_found", response.code)
    }

    // --- Пометка дубликата сессии

    @Test
    fun `Зафиксированный дубликат ставит пометку со временем`() {
        val agent = world.agent(tenant)
        val first = online(agent)
        val clone = world.connect(agent)
        clone.hello()
        assertEquals(Ended(Status.Code.ALREADY_EXISTS, "AGENT_DUPLICATE_SESSION"), clone.ended())

        clock.now = T0.plusSeconds(5)
        first.progress("alive")

        awaitDuplicateMark(agent.agentId, T0.plusSeconds(5))
        assertEquals(T0.plusSeconds(5).toString(), listed(agent.agentId).path("duplicateSessionAt").asString())
    }

    private fun awaitDuplicateMark(
        agent: UUID,
        at: java.time.Instant,
    ) {
        val deadline = System.nanoTime() + Duration.ofSeconds(10).toNanos()
        while (card(agent).json.path("duplicateSessionAt").asString() != at.toString()) {
            check(System.nanoTime() < deadline) { "no duplicate mark at $at" }
            Thread.sleep(POLL_MILLIS)
        }
    }

    @Test
    fun `Отказ второму стриму без ответа держателя пометку не ставит`() {
        val agent = world.agent(tenant)
        online(agent)
        val clone = world.connect(agent)
        clone.hello()
        assertEquals(Ended(Status.Code.ALREADY_EXISTS, "AGENT_DUPLICATE_SESSION"), clone.ended())

        Thread.sleep(QUIET_MILLIS)

        assertTrue(card(agent.agentId).json.path("duplicateSessionAt").isNull)
    }

    @Test
    fun `Замена молчащей сессии новой пометку не ставит`() {
        val agent = world.agent(tenant)
        val first = online(agent)
        clock.now = T0.plusSeconds(61)

        online(agent)

        assertEquals(Ended(Status.Code.UNAVAILABLE, "SESSION_REPLACED"), first.ended())
        assertTrue(card(agent.agentId).json.path("duplicateSessionAt").isNull)
    }

    @Test
    fun `Повторный дубликат обновляет время пометки`() {
        val agent = world.agent(tenant)
        world.jdbc.update("update agents set duplicate_session_at = ? where id = ?", Timestamp.from(T0), agent.agentId)
        assertEquals(T0.toString(), card(agent.agentId).json.path("duplicateSessionAt").asString())
        val first = online(agent)
        val clone = world.connect(agent)
        clone.hello()
        clone.ended()

        clock.now = T0.plus(Duration.ofHours(1))
        first.progress("alive")

        awaitDuplicateMark(agent.agentId, T0.plus(Duration.ofHours(1)))
    }

    @Test
    fun `Агент без дубликатов показан без пометки`() {
        val agent = world.agent(tenant)

        assertTrue(listed(agent.agentId).path("duplicateSessionAt").isNull)
    }

    // --- Отзыв агента

    private fun revoke(
        agent: UUID,
        session: ApiSession = admin,
    ) = world.api.post("/api/v1/agents/$agent/revoke", session, null)

    @Test
    fun `Отзыв агента отвечает 200 с карточкой отозванного агента`() {
        val agent = world.agent(tenant)

        val response = revoke(agent.agentId)

        assertEquals(200, response.status)
        assertEquals(T0.toString(), response.json.path("revokedAt").asString())
        assertEquals("offline", response.json.path("status").asString())
    }

    @Test
    fun `Отзыв агента отзывает все его сертификаты`() {
        val agent = world.agent(tenant)
        val before = T0.minus(Duration.ofDays(1))
        for ((serial, revoked) in listOf("a".repeat(32) to null, "b".repeat(32) to Timestamp.from(before))) {
            world.jdbc.update(
                "insert into agent_certificates (serial, tenant_id, agent_id, issued_at, not_after, revoked_at) " +
                    "values (?, ?, ?, ?, ?, ?)",
                serial,
                tenant,
                agent.agentId,
                Timestamp.from(before),
                Timestamp.from(T0.plus(Duration.ofDays(30))),
                revoked,
            )
        }

        revoke(agent.agentId)

        val revokedAt =
            world.jdbc
                .queryForList("select serial, revoked_at from agent_certificates where agent_id = ?", agent.agentId)
                .associate { it["serial"] as String to (it["revoked_at"] as java.sql.Timestamp).toInstant() }
        assertEquals(3, revokedAt.size)
        assertEquals(before, revokedAt["b".repeat(32)])
        assertEquals(2, revokedAt.values.count { it == T0 })
    }

    @Test
    fun `Отзыв закрывает открытый стрим агента сразу, без периодической проверки`() {
        val agent = world.agent(tenant)
        val stream = online(agent)

        val response = revoke(agent.agentId)

        assertEquals(200, response.status)
        assertEquals(Ended(Status.Code.UNAUTHENTICATED, "AGENT_REVOKED"), stream.ended())
        assertEquals("offline", card(agent.agentId).json.path("status").asString())
    }

    @Test
    fun `Отозванный агент не может подключиться снова`() {
        val agent = world.agent(tenant)
        revoke(agent.agentId)

        val stream = world.connect(agent)
        stream.hello()

        assertEquals(Ended(Status.Code.UNAUTHENTICATED, "CERT_REVOKED"), stream.ended())
    }

    @Test
    fun `Отзыв офлайн-агента отвечает 200`() {
        val agent = world.agent(tenant)

        assertEquals(T0.toString(), revoke(agent.agentId).json.path("revokedAt").asString())
    }

    @Test
    fun `Повторный отзыв агента отвечает 200 и ничего не меняет`() {
        val agent = world.agent(tenant)
        revoke(agent.agentId)
        val certificates =
            world.jdbc.queryForList(
                "select serial, revoked_at from agent_certificates where agent_id = ?",
                agent.agentId,
            )
        clock.now = T0.plus(Duration.ofHours(1))

        val response = revoke(agent.agentId)

        assertEquals(200, response.status)
        assertEquals(T0.toString(), response.json.path("revokedAt").asString())
        assertEquals(
            certificates,
            world.jdbc.queryForList(
                "select serial, revoked_at from agent_certificates where agent_id = ?",
                agent.agentId,
            ),
        )
    }

    @Test
    fun `Отзыв агента другого тенанта не находит его и не трогает стрим`() {
        val agent = world.agent(tenant)
        val stream = online(agent)
        val other = world.admin()

        val response = revoke(agent.agentId, other)

        assertEquals(404, response.status)
        assertEquals("not_found", response.code)
        Thread.sleep(QUIET_MILLIS)
        assertTrue(stream.isOpen)
        assertTrue(card(agent.agentId).json.path("revokedAt").isNull)
    }

    @Test
    fun `Отозванный агент остаётся в списке с пометкой`() {
        val agent = world.agent(tenant)
        revoke(agent.agentId)

        val item = listed(agent.agentId)

        assertEquals(T0.toString(), item.path("revokedAt").asString())
        assertEquals("offline", item.path("status").asString())
    }

    private companion object {
        const val POLL_MILLIS = 20L
        const val QUIET_MILLIS = 300L
    }
}
