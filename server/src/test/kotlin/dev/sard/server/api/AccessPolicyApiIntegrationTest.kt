// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.extension.FixturePolicy
import dev.sard.server.extension.FixturePolicyConfiguration
import dev.sard.server.extension.Operation
import dev.sard.server.extension.Subject
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val DELETE_SOURCE = "DELETE /api/v1/sources/{sourceId}"

/** Rule "Политика расширения может запретить операцию, и запрет закрыт" (server-core-extensions.feature). */
@RestApiTest
@Import(FixturePolicyConfiguration::class)
class AccessPolicyApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @Autowired private val policy: FixturePolicy,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val agent = world.agent(tenant)
    private val api = world.api

    @BeforeTest
    fun `the policy starts clean`() = policy.reset()

    @AfterTest
    fun `drop the tenants`() {
        policy.reset()
        world.close()
    }

    private fun source(): UUID {
        val created = api.send("POST", "/api/v1/sources", admin, sourceJson("s-${UUID.randomUUID()}", agent.agentId))
        assertEquals(201, created.status, created.toString())
        return UUID.fromString(created.json.path("id").asString())
    }

    private fun exists(id: UUID) = api.get("/api/v1/sources/$id", admin).status == 200

    @Test
    fun `Запрещённая расширением операция отвечает 403 access_denied`() {
        val id = source()
        policy.denied = setOf(DELETE_SOURCE)

        val response = api.send("DELETE", "/api/v1/sources/$id", admin)

        assertEquals(403, response.status, response.toString())
        assertEquals("access_denied", response.code)
        assertTrue(exists(id))
    }

    @Test
    fun `Запрет одной операции не мешает другим`() {
        val id = source()
        policy.denied = setOf(DELETE_SOURCE)

        assertEquals(200, api.get("/api/v1/sources/$id", admin).status)
    }

    @Test
    fun `Политика получает субъекта сессии и операцию`() {
        policy.reset()

        api.get("/api/v1/sources", admin)

        assertEquals(listOf(Subject("administrator", "admin") to Operation("GET", "/api/v1/sources")), policy.calls)
    }

    @Test
    fun `Без сессии политика не вызывается и ответ 401`() {
        val response = api.get("/api/v1/sources", null)

        assertEquals(401, response.status)
        assertEquals("unauthenticated", response.code)
        assertTrue(policy.calls.isEmpty())
    }

    @Test
    fun `Чужой Origin отклоняется раньше политики`() {
        val id = source()
        policy.denied = setOf(DELETE_SOURCE)

        val foreign = mapOf("Origin" to "https://evil.example")
        val response = api.sendUnchecked("DELETE", "/api/v1/sources/$id", admin, headers = foreign)

        assertEquals(403, response.status)
        assertEquals("origin_rejected", response.code)
    }

    @Test
    fun `Сбой политики закрывает доступ`() {
        val id = source()
        policy.failing = true
        var response: ApiResponse? = null

        val logs = captureLogs { response = api.send("DELETE", "/api/v1/sources/$id", admin) }

        assertEquals(403, response!!.status)
        assertEquals("access_denied", response.code)
        assertEquals(1, logs.count { "access policy failed" in it }, logs.toString())
        policy.reset()
        assertTrue(exists(id))
    }

    @Test
    fun `Публичные операции и выход политике не подчинены`() {
        policy.denyAll = true

        assertEquals(200, api.get("/api/v1/status", null).status)
        assertEquals(200, api.get("/api/v1/onboarding", null).status)
        val signedIn = api.signIn(tenant)
        assertEquals(204, api.send("DELETE", "/api/v1/session", signedIn).status)
        assertTrue(policy.calls.isEmpty(), policy.calls.toString())
    }

    @Test
    fun `Сведения о сервере подчинены политике`() {
        policy.denied = setOf("GET /api/v1/server")

        val response = api.get("/api/v1/server", admin)

        assertEquals(403, response.status)
        assertEquals("access_denied", response.code)
    }

    // ---- spring-security.feature: Правило "Политика доступа спрашивается в цепочке один раз" ----

    @Test
    fun `Неизвестный путь под api v1 с сессией отвечает 404 и не спрашивает политику`() {
        policy.denyAll = true

        val response = api.sendUnchecked("GET", "/api/v1/no-such-resource", admin)

        assertEquals(404, response.status, response.toString())
        assertEquals("application/problem+json", response.contentType)
        assertTrue(policy.calls.isEmpty(), policy.calls.toString())
    }

    @Test
    fun `Метод без операции под api v1 отвечает как у ядра и не спрашивает политику`() {
        val id = source()
        val unmapped = { api.sendUnchecked("PATCH", "/api/v1/sources/$id", admin) }
        val asked = unmapped()
        policy.reset()
        policy.denyAll = true

        val denied = unmapped()

        assertEquals(asked.status, denied.status)
        assertEquals(asked.code, denied.code)
        assertTrue(policy.calls.isEmpty(), policy.calls.toString())
    }

    @Test
    fun `Операция, обработчик которой ответил ошибкой, спрашивает политику один раз`() {
        val response = api.get("/api/v1/sources/0192f7a0-0000-7000-8000-000000000999", admin)

        assertEquals(404, response.status, response.toString())
        assertEquals("application/problem+json", response.contentType)
        assertEquals(1, policy.calls.size, policy.calls.toString())
        assertEquals(Operation("GET", "/api/v1/sources/{sourceId}"), policy.calls.single().second)
    }

    @Test
    fun `Отказ политики приходит ответом access_denied, а не страницей ошибки`() {
        policy.denied = setOf("GET /api/v1/sources")

        val response = api.get("/api/v1/sources", admin)

        assertEquals(403, response.status)
        assertEquals("application/problem+json", response.contentType)
        assertEquals("access_denied", response.code)
    }
}
