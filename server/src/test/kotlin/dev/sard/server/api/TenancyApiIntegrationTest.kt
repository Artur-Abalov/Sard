// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val EVIL = "https://evil.example"

/** Rules "Без сессии API закрыт ...", "Тенант только из сессии ...", "Отозванный агент получает код agent_revoked". */
@RestApiTest
class TenancyApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired private val mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val a = world.tenant()
    private val b = world.tenant()
    private val adminA = world.admin(a)
    private val adminB = world.admin(b)

    @AfterTest
    fun `drop the tenants`() = world.close()

    /** Everything one tenant can own, created through the API as its administrator would. */
    private inner class Owned(
        val tenant: UUID,
        val admin: ApiSession,
    ) {
        val agent = world.agent(tenant)
        val token = tokens.create(tenant).id.toString()
        val source =
            world.api
                .post("/api/v1/sources", admin, body("etc", agent.agentId))
                .json
                .path("id")
                .asString()
        val run =
            world.api
                .post("/api/v1/sources/$source/runs", admin, null)
                .json
                .path("id")
                .asString()
        val step = world.jdbc.queryForObject("select id from run_steps where run_id = ?", String::class.java, UUID.fromString(run))!!
    }

    private fun body(
        name: String,
        agent: UUID,
    ) = """{"name":"$name","agentId":"$agent","plugin":"files","repositoryName":"qa","config":{"paths":["/etc"]}}"""

    private fun counts(): List<Int> =
        listOf("enrollment_tokens", "agents", "sources", "runs").map {
            world.jdbc.queryForObject("select count(*) from $it", Int::class.java)!!
        }

    private fun operations(
        o: Owned,
        random: UUID = UUID.randomUUID(),
    ): List<Triple<String, String, String?>> =
        listOf(
            Triple("GET", "/api/v1/agents", null),
            Triple("GET", "/api/v1/agents/${o.agent.agentId}", null),
            Triple("POST", "/api/v1/agents/${o.agent.agentId}/revoke", null),
            Triple("POST", "/api/v1/enrollment-tokens", "{}"),
            Triple("GET", "/api/v1/enrollment-tokens", null),
            Triple("GET", "/api/v1/enrollment-tokens/${o.token}", null),
            Triple("POST", "/api/v1/enrollment-tokens/${o.token}/revoke", null),
            Triple("GET", "/api/v1/sources", null),
            Triple("POST", "/api/v1/sources", body("new", o.agent.agentId)),
            Triple("GET", "/api/v1/sources/${o.source}", null),
            Triple("PUT", "/api/v1/sources/${o.source}", body("etc", o.agent.agentId)),
            Triple("DELETE", "/api/v1/sources/${o.source}", null),
            Triple("POST", "/api/v1/sources/${o.source}/runs", null),
            Triple("GET", "/api/v1/sources/${o.source}/snapshots", null),
            Triple("GET", "/api/v1/runs", null),
            Triple("GET", "/api/v1/runs/${o.run}", null),
            Triple("GET", "/api/v1/runs/${o.run}/steps/${o.step}/logs", null),
        ).also { assertTrue(random != o.tenant) }

    @Test
    fun `Операция без сессии отвечает 401 и ничего не меняет`() {
        val owned = Owned(a, adminA)
        val before = counts()

        for ((method, path, body) in operations(owned)) {
            val response = world.api.send(method, path, null, body)

            assertEquals(401, response.status, "$method $path")
            assertEquals("unauthenticated", response.code, "$method $path")
        }
        assertEquals(before, counts())
    }

    @Test
    fun `Изменяющая операция с чужим Origin отвечает 403 и ничего не меняет`() {
        val owned = Owned(a, adminA)
        val before = counts()
        val state = world.api.get("/api/v1/sources/${owned.source}", adminA).json

        for ((method, path, body) in operations(owned).filter { it.first != "GET" }) {
            val response = world.api.send(method, path, adminA, body, mapOf("Origin" to EVIL))

            assertEquals(403, response.status, "$method $path")
            assertEquals("origin_rejected", response.code, "$method $path")
        }
        assertEquals(before, counts())
        assertEquals(state, world.api.get("/api/v1/sources/${owned.source}", adminA).json)
    }

    @Test
    fun `Ни одна операция этапа 1 больше не отвечает 501`() {
        val owned = Owned(a, adminA)

        for ((method, path, body) in operations(owned)) {
            val response = world.api.send(method, path, adminA, body)

            assertTrue(response.status != 501 && response.code != "not_implemented", "$method $path: $response")
        }
    }

    @Test
    fun `Объект другого тенанта по прямому id отвечает 404`() {
        val owned = Owned(a, adminA)
        val foreign = Owned(b, adminB)
        val randomId = UUID.randomUUID()
        val byId =
            operations(owned).filter { (_, path, _) ->
                owned.agent.agentId.toString() in path || owned.token in path || owned.source in path ||
                    owned.run in path
            }
        val before = counts()

        for ((method, path, body) in byId) {
            val response = world.api.send(method, path, foreign.admin, body)
            val unknown = world.api.send(method, path.replace(Regex("[0-9a-f]{8}-[0-9a-f-]{27}"), randomId.toString()), foreign.admin, body)

            assertEquals(404, response.status, "$method $path: $response")
            assertEquals("not_found", response.code)
            assertEquals(unknown.body, response.body, "$method $path")
        }
        assertEquals(before, counts().let { it }.also { assertTrue(it.isNotEmpty()) })
        assertEquals(
            "active",
            world.api
                .get("/api/v1/enrollment-tokens/${owned.token}", adminA)
                .json
                .path("status")
                .asString(),
        )
        assertTrue(
            world.api
                .get("/api/v1/agents/${owned.agent.agentId}", adminA)
                .json
                .path("revokedAt")
                .isNull,
        )
        assertEquals(200, world.api.get("/api/v1/sources/${owned.source}", adminA).status)
        assertEquals(
            0,
            world.api
                .get("/api/v1/runs?sourceId=${owned.source}", adminB)
                .json
                .path("items")
                .size(),
        )
    }

    @Test
    fun `Список не содержит объектов другого тенанта`() {
        Owned(a, adminA)

        for (path in listOf("/api/v1/agents", "/api/v1/enrollment-tokens", "/api/v1/sources", "/api/v1/runs")) {
            val json = world.api.get(path, adminB).json

            assertTrue(json.path("items").isEmpty, path)
            assertTrue(json.path("nextCursor").isNull, path)
        }
    }

    @Test
    fun `Фильтр по id агента другого тенанта даёт пустой список, а не ошибку`() {
        val owned = Owned(a, adminA)

        assertTrue(
            world.api
                .get("/api/v1/sources?agentId=${owned.agent.agentId}", adminB)
                .json
                .path("items")
                .isEmpty,
        )
        assertTrue(
            world.api
                .get("/api/v1/runs?agentId=${owned.agent.agentId}", adminB)
                .json
                .path("items")
                .isEmpty,
        )
    }

    @Test
    fun `Источник с агентом другого тенанта отклоняется как неизвестный агент`() {
        val owned = Owned(a, adminA)
        val sourcesBefore = world.count("sources", a) + world.count("sources", b)

        val response = world.api.post("/api/v1/sources", adminB, body("etc", owned.agent.agentId))

        assertEquals(422, response.status)
        assertEquals("unknown_agent", response.code)
        assertEquals(listOf("agentId"), response.errorFields())
        assertEquals(sourcesBefore, world.count("sources", a) + world.count("sources", b))
    }

    @Test
    fun `Созданный объект попадает в тенант сессии`() {
        val agent = world.agent(a)

        val token =
            world.api
                .post("/api/v1/enrollment-tokens", adminA)
                .json
                .path("id")
                .asString()
        val source =
            world.api
                .post("/api/v1/sources", adminA, body("etc", agent.agentId))
                .json
                .path("id")
                .asString()

        assertEquals(
            1,
            world.jdbc.queryForObject(
                "select count(*) from enrollment_tokens where id = ? and tenant_id = ?",
                Int::class.java,
                UUID.fromString(token),
                a,
            ),
        )
        assertEquals(
            1,
            world.jdbc.queryForObject(
                "select count(*) from sources where id = ? and tenant_id = ?",
                Int::class.java,
                UUID.fromString(source),
                a,
            ),
        )
        assertEquals(
            0,
            world.api
                .get("/api/v1/sources", adminB)
                .json
                .path("items")
                .size(),
        )
        assertEquals(404, world.api.get("/api/v1/enrollment-tokens/$token", adminB).status)
    }

    @Test
    fun `Отозванный агент получает код agent_revoked в каждой операции`() {
        val owned = Owned(a, adminA)
        world.api.post("/api/v1/agents/${owned.agent.agentId}/revoke", adminA, null)
        world.forceRun(UUID.fromString(owned.run), "failed", "agent revoked")

        val create = world.api.post("/api/v1/sources", adminA, body("other", owned.agent.agentId))
        val replace = world.api.send("PUT", "/api/v1/sources/${owned.source}", adminA, body("etc", owned.agent.agentId))
        val run = world.api.post("/api/v1/sources/${owned.source}/runs", adminA, null)

        assertEquals(listOf(422, 422, 409), listOf(create.status, replace.status, run.status))
        assertEquals(listOf("agent_revoked", "agent_revoked", "agent_revoked"), listOf(create.code, replace.code, run.code))
    }
}
