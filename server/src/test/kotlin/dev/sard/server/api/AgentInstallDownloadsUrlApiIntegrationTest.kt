// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.install.SignedRelease
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.TestPropertySource
import tools.jackson.databind.ObjectMapper
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** Rule "Адрес сервера в командах": SARD_AGENT_DOWNLOADS_URL is https://sard.corp.example. */
@RestApiTest
@Import(SignedRelease::class)
@TestPropertySource(
    properties = [
        "sard.pki.server-names=sard.corp.example,localhost,127.0.0.1,::1",
        "sard.agent-packages.downloads-url=https://sard.corp.example",
    ],
)
class AgentInstallDownloadsUrlApiIntegrationTest(
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
    fun `SARD_AGENT_DOWNLOADS_URL задаёт адрес раздачи`() {
        val links = downloads(install())

        assertEquals(3, links.size)
        assertTrue(links.all { " https://sard.corp.example/downloads/agent/" in it }, links.toString())
    }
}
