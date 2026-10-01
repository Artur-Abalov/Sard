// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentRejectedException
import dev.sard.server.enrollment.EnrollmentToken
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.TestPropertySource
import tools.jackson.databind.ObjectMapper
import java.time.Duration
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val SECRET_PREFIX = 16

/** Rule "Токены регистрации — по правилам S2b, строка только в ответе на создание" over HTTP. */
@RestApiTest
class EnrollmentTokensApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired private val enrollment: Enrollment,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired private val mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Autowired @org.springframework.beans.factory.annotation.Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val clock = world.clock
    private val admin = world.admin()

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun create(body: String = "{}") = world.api.post("/api/v1/enrollment-tokens", admin, body)

    @Test
    fun `Создание токена отвечает 201 со строкой, командой и сроком`() {
        val response = create()

        assertEquals(201, response.status)
        val token = response.json.path("token").asString()
        EnrollmentToken.parse(token)
        val command = response.json.path("enrollCommand").asString()
        assertTrue(command.startsWith("sard-agent enroll --server ") && command.endsWith(" --token $token"), command)
        assertEquals(T0.plus(Duration.ofHours(24)).toString(), response.json.path("expiresAt").asString())
    }

    @Test
    fun `Ответ на создание говорит, задан ли адрес для агентов явно`() {
        assertEquals(false, create().json.path("agentEndpointConfigured").asBoolean())
    }

    @Test
    fun `Срок токена на границе принимается`() {
        for ((seconds, expires) in listOf(300L to T0.plusSeconds(300), 604800L to T0.plusSeconds(604800))) {
            val response = create("""{"ttlSeconds":$seconds}""")
            assertEquals(201, response.status)
            assertEquals(expires.toString(), response.json.path("expiresAt").asString())
        }
    }

    @Test
    fun `Срок токена вне диапазона отклоняется ошибкой у поля ttlSeconds`() {
        for (seconds in listOf(0, -60, 299, 604801)) {
            val response = create("""{"ttlSeconds":$seconds}""")
            assertEquals(422, response.status, "$seconds")
            assertEquals("validation_failed", response.code)
            assertEquals(listOf("ttlSeconds"), response.errorFields())
        }
        assertEquals(0, world.count("enrollment_tokens", admin.tenant))
    }

    @Test
    fun `Подпись токена показывается в списке и карточке`() {
        val long = "я".repeat(200)
        val cases = listOf("db1 — бухгалтерия" to "db1 — бухгалтерия", "" to null, null to null, long to long)
        for ((label, shown) in cases) {
            val body = if (label == null) "{}" else mapper.writeValueAsString(mapOf("label" to label))
            val id =
                create(body)
                    .also { assertEquals(201, it.status) }
                    .json
                    .path("id")
                    .asString()
            val card = world.api.get("/api/v1/enrollment-tokens/$id", admin).json
            assertEquals(shown, card.path("label").takeIf { !it.isNull }?.asString(), "card of $label")
            val inList =
                world.api
                    .get("/api/v1/enrollment-tokens", admin)
                    .json
                    .path("items")
                    .first { it.path("id").asString() == id }
            assertEquals(shown, inList.path("label").takeIf { !it.isNull }?.asString(), "list of $label")
        }
    }

    @Test
    fun `Подпись токена длиннее 200 символов отклоняется`() {
        val response = create("""{"label":"${"x".repeat(201)}"}""")

        assertEquals(422, response.status)
        assertEquals("validation_failed", response.code)
        assertEquals(listOf("label"), response.errorFields())
        assertEquals(0, world.count("enrollment_tokens", admin.tenant))
    }

    @Test
    fun `Список и карточка токена не содержат строку токена и команду`() {
        val created = create().json
        val token = created.path("token").asString()
        val secret = token.removePrefix("sard_").substringBefore('.')
        val id = created.path("id").asString()

        val bodies =
            listOf(world.api.get("/api/v1/enrollment-tokens", admin).body, world.api.get("/api/v1/enrollment-tokens/$id", admin).body)

        for (body in bodies) {
            assertTrue(token !in body && secret !in body && "enrollCommand" !in body, body)
        }
    }

    @Test
    fun `Список токенов фильтруется по статусу`() {
        val active = create().json.path("id").asString()
        val used = world.enroll(admin.tenant).let { usedTokenOf(it.agentId) }
        val expired = tokens.create(admin.tenant, Duration.ofMinutes(5)).id
        val revoked =
            create()
                .json
                .path("id")
                .asString()
                .also { world.api.post("/api/v1/enrollment-tokens/$it/revoke", admin, null) }
        clock.now = T0.plus(Duration.ofMinutes(10))

        fun ids(status: String) =
            world.api
                .get("/api/v1/enrollment-tokens?status=$status", admin)
                .json
                .pluck("items", "id")

        assertEquals(listOf(expired.toString()), ids("expired"))
        assertEquals(listOf(used), ids("used"))
        assertEquals(listOf(revoked), ids("revoked"))
        assertEquals(listOf(active), ids("active"))
    }

    @Test
    fun `Карточка использованного токена называет агента`() {
        val agent = world.enroll(admin.tenant)
        val id = usedTokenOf(agent.agentId)

        val card = world.api.get("/api/v1/enrollment-tokens/$id", admin).json

        assertEquals("used", card.path("status").asString())
        assertEquals(T0.toString(), card.path("usedAt").asString())
        assertEquals(agent.agentId.toString(), card.path("agentId").asString())
    }

    @Test
    fun `Отзыв активного токена отвечает 200 с карточкой отозванного`() {
        val created = create().json
        val id = created.path("id").asString()

        val response = world.api.post("/api/v1/enrollment-tokens/$id/revoke", admin, null)

        assertEquals(200, response.status)
        assertEquals("revoked", response.json.path("status").asString())
        assertEquals(T0.toString(), response.json.path("revokedAt").asString())
        val token = created.path("token").asString()
        val refused = assertFailsWith<EnrollmentRejectedException> { enrollment.enroll(token, world.csr(), "db1") }
        assertEquals(EnrollmentRejectedException.Reason.TOKEN_REVOKED, refused.reason)
    }

    @Test
    fun `Повторный отзыв токена отвечает 200 и ничего не меняет`() {
        val id = create().json.path("id").asString()
        world.api.post("/api/v1/enrollment-tokens/$id/revoke", admin, null)
        clock.now = T0.plus(Duration.ofMinutes(10))

        val response = world.api.post("/api/v1/enrollment-tokens/$id/revoke", admin, null)

        assertEquals(200, response.status)
        assertEquals("revoked", response.json.path("status").asString())
        assertEquals(T0.toString(), response.json.path("revokedAt").asString())
    }

    @Test
    fun `Отзыв использованного токена отвечает 409 token_used с агентом`() {
        val agent = world.enroll(admin.tenant)
        val id = usedTokenOf(agent.agentId)

        val response = world.api.post("/api/v1/enrollment-tokens/$id/revoke", admin, null)

        assertEquals(409, response.status)
        assertEquals("token_used", response.code)
        assertEquals(agent.agentId.toString(), response.json.path("agentId").asString())
        val card = world.api.get("/api/v1/enrollment-tokens/$id", admin).json
        assertEquals("used", card.path("status").asString())
        assertEquals(agent.agentId.toString(), card.path("agentId").asString())
    }

    @Test
    fun `Отзыв истёкшего токена отвечает 409 token_expired`() {
        val id = tokens.create(admin.tenant, Duration.ofMinutes(5)).id
        clock.now = T0.plus(Duration.ofMinutes(5))

        val response = world.api.post("/api/v1/enrollment-tokens/$id/revoke", admin, null)

        assertEquals(409, response.status)
        assertEquals("token_expired", response.code)
        assertTrue(response.json.path("agentId").isNull)
        val card = world.api.get("/api/v1/enrollment-tokens/$id", admin).json
        assertEquals("expired", card.path("status").asString())
        assertTrue(card.path("revokedAt").isNull)
    }

    @Test
    fun `Отзыв несуществующего токена отвечает 404`() {
        val response = world.api.post("/api/v1/enrollment-tokens/${UUID.randomUUID()}/revoke", admin, null)

        assertEquals(404, response.status)
        assertEquals("not_found", response.code)
    }

    private fun usedTokenOf(agent: UUID): String =
        world.jdbc.queryForObject("select id from enrollment_tokens where agent_id = ?", String::class.java, agent)!!

    @Test
    fun `Строка токена не попадает в логи сервера при операциях API`() {
        val operations: List<(String) -> Unit> =
            listOf(
                {},
                { world.api.get("/api/v1/enrollment-tokens", admin) },
                { id -> world.api.get("/api/v1/enrollment-tokens/$id", admin) },
                { id -> world.api.post("/api/v1/enrollment-tokens/$id/revoke", admin, null) },
            )
        for (operation in operations) {
            var token = ""
            val logs =
                captureLogs {
                    val created = create().json
                    token = created.path("token").asString()
                    operation(created.path("id").asString())
                }
            val joined = logs.joinToString("\n")
            // Spring logs a body cut to its first 100 characters at DEBUG: a prefix of the secret is a leak too.
            val secret = token.removePrefix("sard_").substringBefore('.')
            assertFalse(token in joined || secret.take(SECRET_PREFIX) in joined, "token leaked: $joined")
        }
    }
}
