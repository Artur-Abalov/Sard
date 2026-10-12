// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.AgentRole
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
import kotlin.test.assertNull

/** V1, rules "Источник привязывается только к агенту-источнику" and "Сервер не отправляет агенту шаг ... роль". */
@RestApiTest
class AgentRoleSourcesApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @Autowired private val hellos: ReconciledHellos,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val source = world.agent(tenant)
    private val verifier = world.agent(tenant, role = AgentRole.VERIFIER)

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun sourceBody(
        agentId: UUID,
        plugin: String,
        repository: String,
    ) = """{"name":"etc","agentId":"$agentId","plugin":"$plugin","repositoryName":"$repository",""" +
        """"config":{"paths":["/etc"]}}"""

    private fun create(
        agentId: UUID,
        plugin: String = "files",
        repository: String = "qa",
        session: ApiSession = admin,
    ) = world.api.post(
        "/api/v1/sources",
        session,
        sourceBody(agentId, plugin, repository),
    )

    private fun assertRoleMismatch(response: ApiResponse) {
        assertEquals(422, response.status, response.toString())
        assertEquals("agent_role_mismatch", response.code)
        assertEquals(listOf("agentId"), response.errorFields())
    }

    @Test
    fun `Источник с агентом-верификатором отклоняется кодом agent_role_mismatch при создании`() {
        assertRoleMismatch(create(verifier.agentId))
        assertEquals(0, world.count("sources", tenant))
    }

    @Test
    fun `Источник с агентом-верификатором отклоняется кодом agent_role_mismatch при замене`() {
        val id = create(source.agentId).json.path("id").asString()
        val before = world.api.get("/api/v1/sources/$id", admin).json

        val response =
            world.api.send(
                "PUT",
                "/api/v1/sources/$id",
                admin,
                sourceBody(verifier.agentId, "files", "qa"),
            )

        assertRoleMismatch(response)
        assertEquals(before, world.api.get("/api/v1/sources/$id", admin).json)
    }

    @Test
    fun `Источник с агентом-источником создаётся как раньше`() {
        assertEquals(201, create(source.agentId).status)
    }

    @Test
    fun `Отозванный верификатор получает agent_revoked, а не agent_role_mismatch`() {
        world.api.post("/api/v1/agents/${verifier.agentId}/revoke", admin, null)

        val response = create(verifier.agentId)

        assertEquals(422, response.status)
        assertEquals("agent_revoked", response.code)
    }

    @Test
    fun `Роль проверяется раньше плагина и репозитория`() {
        assertRoleMismatch(create(verifier.agentId, plugin = "postgres", repository = "offsite"))
    }

    @Test
    fun `Верификатор другого тенанта отклоняется как неизвестный агент`() {
        val other = world.tenant()

        val response = create(verifier.agentId, session = world.admin(other))

        assertEquals(422, response.status)
        assertEquals("unknown_agent", response.code)
        assertEquals(listOf("agentId"), response.errorFields())
    }

    @Test
    fun `Запуск источника, привязанного к верификатору в обход API, отклоняется`() {
        val id = create(source.agentId).json.path("id").asString()
        world.jdbc.update("update sources set agent_id = ? where id = ?::uuid", verifier.agentId, id)
        val stream = world.connect(verifier).also { it.hello() }
        hellos.await(verifier.agentId)

        val response = world.api.post("/api/v1/sources/$id/runs", admin, null)

        assertEquals(409, response.status)
        assertEquals("agent_role_mismatch", response.code)
        assertEquals(0, world.count("runs", tenant))
        assertEquals(0, world.count("run_steps", tenant))
        assertNull(stream.received.poll(300, java.util.concurrent.TimeUnit.MILLISECONDS)?.takeIf { it.hasRunStep() })
    }
}
