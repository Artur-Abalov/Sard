// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import dev.sard.server.role.AgentRole
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** V1, rule "Роль агента видна в REST" and "machine_id не возвращается в REST". */
@RestApiTest
class AgentRoleAgentsApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun card(agent: UUID) = world.api.get("/api/v1/agents/$agent", admin)

    private fun listed() = world.api.get("/api/v1/agents?limit=200", admin)

    @Test
    fun `Список и карточка агентов показывают роль`() {
        val source = world.agent(tenant)
        val verifier = world.agent(tenant, role = AgentRole.VERIFIER)
        val list =
            listed()
                .json
                .path("items")
                .list()
                .associate { it.path("id").asString() to it.path("role").asString() }

        assertEquals("source", list[source.agentId.toString()])
        assertEquals("verifier", list[verifier.agentId.toString()])
        assertEquals("source", card(source.agentId).json.path("role").asString())
        assertEquals("verifier", card(verifier.agentId).json.path("role").asString())
    }

    @Test
    fun `Встроенный агент показывается с ролью source`() {
        val builtin = world.agent(tenant, builtin = true)

        val json = card(builtin.agentId).json

        assertTrue(json.path("builtin").asBoolean())
        assertEquals("source", json.path("role").asString())
    }

    @Test
    fun `Отзыв верификатора отвечает карточкой с ролью`() {
        val verifier = world.agent(tenant, role = AgentRole.VERIFIER)

        val response = world.api.post("/api/v1/agents/${verifier.agentId}/revoke", admin, null)

        assertEquals(200, response.status)
        assertEquals("verifier", response.json.path("role").asString())
        assertEquals(T0.toString(), response.json.path("revokedAt").asString())
    }

    @Test
    fun `machine_id не возвращается в REST`() {
        val agent = world.agent(tenant, snapshotOf(machineId = MACHINE_ID))

        val bodies = listOf(listed().body, card(agent.agentId).body)

        for (body in bodies) {
            assertFalse(MACHINE_ID in body, body)
            assertFalse("machineId" in body, body)
            assertFalse("machine_id" in body, body)
        }
    }
}
