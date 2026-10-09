// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.agents.stream.Ended
import dev.sard.server.agents.stream.TestAgent
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
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val CONFIRMATION_REQUIRED = "self_agent_confirmation_required"

/**
 * Rules "Встроенный токен не виден и не управляется через API", "Встроенный агент виден в API...",
 * "Обзор считает встроенного агента, как любого, а встроенный токен - нет" and "Отзыв встроенного агента
 * требует подтверждения sard-self" of docs/specs/server/self-agent.feature.
 */
@RestApiTest
class SelfAgentApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired private val mapper: ObjectMapper,
    @Autowired private val hellos: ReconciledHellos,
    @LocalServerPort private val port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val clock = world.clock
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun online(agent: TestAgent) =
        world.connect(agent).also {
            it.hello()
            hellos.await(agent.agentId)
        }

    private fun revoke(
        agent: UUID,
        query: String = "",
    ) = world.api.post("/api/v1/agents/$agent/revoke$query", admin, null)

    private fun card(agent: UUID) = world.api.get("/api/v1/agents/$agent", admin).json

    private fun overview() = world.api.get("/api/v1/overview", admin).json

    private fun certificateRevocations(agent: UUID) =
        world.jdbc.queryForList("select serial, revoked_at from agent_certificates where agent_id = ?", agent)

    // --- builtin in the API

    @Test
    fun `API показывает признак builtin у агента в списке и в карточке`() {
        val builtin = world.agent(tenant, hostname = "sard-self", builtin = true)
        val ordinary = world.agent(tenant, hostname = "sard-self")

        val listed =
            world.api
                .get("/api/v1/agents", admin)
                .json
                .path("items")
                .list()
                .associate { it.path("id").asString() to it.path("builtin") }

        assertEquals(
            mapOf(builtin.agentId.toString() to true, ordinary.agentId.toString() to false),
            listed.mapValues { it.value.asBoolean() },
        )
        assertTrue(card(builtin.agentId).path("builtin").asBoolean())
        assertEquals(false, card(ordinary.agentId).path("builtin").asBoolean(true))
    }

    @Test
    fun `Встроенного агента нельзя удалить через API`() {
        val builtin = world.agent(tenant, builtin = true)

        val request =
            HttpRequest
                .newBuilder(URI.create("http://localhost:$port/api/v1/agents/${builtin.agentId}"))
                .header("Cookie", "$SESSION_COOKIE=${admin.cookie}")
                .DELETE()
                .build()
        val status = HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString()).statusCode()

        assertTrue(status !in 200..299, "status $status")
        assertNull(card(builtin.agentId).path("revokedAt").takeUnless { it.isNull })
    }

    // --- tokens

    @Test
    fun `Список, карточка и отзыв токенов не видят встроенный токен`() {
        val builtin = tokens.replaceBuiltin(tenant)
        val ordinary = tokens.create(tenant)

        val listed =
            world.api
                .get("/api/v1/enrollment-tokens", admin)
                .json
                .pluck("items", "id")
        val get = world.api.get("/api/v1/enrollment-tokens/${builtin.id}", admin)
        val revoke = world.api.post("/api/v1/enrollment-tokens/${builtin.id}/revoke", admin, null)

        assertEquals(listOf(ordinary.id.toString()), listed)
        assertEquals(listOf(404, "not_found"), listOf(get.status, get.code))
        assertEquals(listOf(404, "not_found"), listOf(revoke.status, revoke.code))
        assertTrue(tokens.builtinUsable(tenant, hashOf(builtin.reveal()), Duration.ZERO))
    }

    @Test
    fun `Создание токена через API не даёт встроенного токена`() {
        world.api.post("/api/v1/enrollment-tokens", admin, """{"builtin":true}""")

        val sql = "select count(*) from enrollment_tokens where tenant_id = ? and builtin"
        assertEquals(0, world.jdbc.queryForObject(sql, Int::class.java, tenant))
    }

    // --- overview

    @Test
    fun `Встроенный агент учитывается в счётчиках и закрывает пункт agentConnected`() {
        val builtin = world.agent(tenant, builtin = true)
        online(builtin)

        val json = overview()

        assertEquals(1, json.path("agentsOnline").asInt())
        assertEquals(1, json.path("agentsTotal").asInt())
        assertTrue(json.path("firstSteps").path("agentConnected").asBoolean())
    }

    @Test
    fun `Встроенный токен не закрывает пункт tokenIssued`() {
        world.agent(tenant, builtin = true)

        assertEquals(false, overview().path("firstSteps").path("tokenIssued").asBoolean(true))
    }

    // --- revoke needs the confirmation

    @Test
    fun `Отзыв встроенного агента без подтверждения отклоняется 409 и ничего не меняет`() {
        val builtin = world.agent(tenant, builtin = true)
        val stream = online(builtin)

        val response = revoke(builtin.agentId)

        assertEquals(409, response.status)
        assertEquals(CONFIRMATION_REQUIRED, response.code)
        assertNull(card(builtin.agentId).path("revokedAt").takeUnless { it.isNull })
        assertEquals("online", card(builtin.agentId).path("status").asString())
        assertEquals(emptyList(), certificateRevocations(builtin.agentId).filter { it["revoked_at"] != null })
        assertNull(stream.endedWithin(Duration.ofMillis(500)))
    }

    @Test
    fun `Неверное подтверждение не отзывает встроенного агента`() {
        val builtin = world.agent(tenant, builtin = true)

        for (value in listOf("", "SARD-SELF", "sard-self%20", "yes")) {
            val response = revoke(builtin.agentId, "?confirm=$value")
            assertEquals(listOf(409, CONFIRMATION_REQUIRED), listOf(response.status, response.code), value)
        }
        assertTrue(card(builtin.agentId).path("revokedAt").isNull)
    }

    @Test
    fun `Отзыв встроенного агента с подтверждением отзывает его как любого агента`() {
        val builtin = world.agent(tenant, builtin = true)
        val stream = online(builtin)

        val response = revoke(builtin.agentId, "?confirm=sard-self")

        assertEquals(200, response.status)
        assertEquals(clock.instant().toString(), response.json.path("revokedAt").asString())
        assertEquals("offline", response.json.path("status").asString())
        assertEquals(Ended(Status.Code.UNAUTHENTICATED, "AGENT_REVOKED"), stream.ended())
        assertTrue(response.json.path("builtin").asBoolean())
    }

    @Test
    fun `Отзыв обычного агента не требует подтверждения и игнорирует параметр`() {
        for (query in listOf("", "?confirm=sard-self", "?confirm=yes")) {
            val agent = world.agent(tenant)
            assertEquals(200, revoke(agent.agentId, query).status, query)
        }
    }

    @Test
    fun `Повторный отзыв отозванного встроенного агента без подтверждения отвечает 200 и ничего не меняет`() {
        val builtin = world.agent(tenant, builtin = true)
        val revokedAt = revoke(builtin.agentId, "?confirm=sard-self").json.path("revokedAt").asString()
        val certificates = certificateRevocations(builtin.agentId)
        clock.now = clock.instant().plus(Duration.ofHours(1))

        val response = revoke(builtin.agentId)

        assertEquals(200, response.status)
        assertEquals(revokedAt, response.json.path("revokedAt").asString())
        assertEquals(certificates, certificateRevocations(builtin.agentId))
        assertEquals(true, card(builtin.agentId).path("builtin").asBoolean())
    }

    @Test
    fun `Неизвестный агент при отзыве с подтверждением даёт 404`() {
        assertEquals(404, revoke(UUID.randomUUID(), "?confirm=sard-self").status)
    }

    private fun hashOf(token: String) =
        dev.sard.server.enrollment.EnrollmentToken
            .parse(token)
            .secret
            .hash()
}
