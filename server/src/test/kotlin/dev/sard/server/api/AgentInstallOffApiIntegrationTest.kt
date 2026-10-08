// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.install.NoDownloads
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** Rules "Выключенная раздача" and "Агент старше раздаваемой версии": SARD_AGENT_DOWNLOADS=false, server v1.4.0. */
@RestApiTest
@Import(NoDownloads::class)
class AgentInstallOffApiIntegrationTest(
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

    private fun install(query: String = "") = world.api.get("/api/v1/agent-install$query", admin)

    private fun downloads(response: ApiResponse): List<String> =
        response.json
            .path("steps")
            .list()
            .filter { it.path("kind").asString() == "download" }
            .flatMap { it.path("commands").list() }
            .map { it.asString() }

    private fun outdated(agent: java.util.UUID) =
        world.api
            .get("/api/v1/agents/$agent", admin)
            .json
            .path("outdated")
            .asBoolean()

    @Test
    fun `При выключенной раздаче шагов нет`() {
        val response = install()

        assertEquals(200, response.status)
        assertFalse(response.json.path("downloadsEnabled").asBoolean())
        assertEquals(emptyList(), response.json.path("steps").list())
        assertTrue(
            response.json
                .path("manualInstallDoc")
                .asString()
                .endsWith("docs/operations/agent-install.md"),
        )
    }

    @Test
    fun `При выключенной раздаче версия агента - версия сервера, restic неизвестен`() {
        val json = install().json

        assertEquals("v1.4.0", json.path("agentVersion").asString())
        assertTrue(json.path("resticVersion").isNull)
        assertFalse(json.path("signed").asBoolean())
    }

    @Test
    fun `При выключенной раздаче агент сравнивается с версией сервера`() {
        val old = world.agent(tenant, snapshotOf(version = "v1.3.2"))
        val current = world.agent(tenant, snapshotOf(version = "v1.4.0"))

        assertTrue(outdated(old.agentId))
        assertFalse(outdated(current.agentId))
    }

    @Test
    fun `При выключенной раздаче обновление не даёт команд`() {
        val agent = world.agent(tenant, snapshotOf())

        val response = world.api.get("/api/v1/agents/${agent.agentId}/upgrade", admin)

        assertEquals(200, response.status)
        assertFalse(response.json.path("downloadsEnabled").asBoolean())
        assertEquals(emptyList(), response.json.path("steps").list())
        assertTrue(response.json.path("reason").isNull)
    }

    @Test
    fun `При выключенной раздаче обновление не обещает сохранить конфигурацию`() {
        val agent = world.agent(tenant, snapshotOf())

        val json = world.api.get("/api/v1/agents/${agent.agentId}/upgrade?format=deb", admin).json

        assertFalse(json.path("downloadsEnabled").asBoolean())
        assertFalse(json.path("keepsConfiguration").asBoolean(true))
    }
}
