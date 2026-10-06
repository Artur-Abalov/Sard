// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.install.UnsignedRelease
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

/** Rule "Проверка подписи": an image built without SHA256SUMS.minisig. */
@RestApiTest
@Import(UnsignedRelease::class)
class AgentInstallUnsignedApiIntegrationTest(
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
    fun `Неподписанный релиз помечен и не даёт шага подписи`() {
        val response = install()

        assertFalse(response.json.path("signed").asBoolean())
        val kinds =
            response.json
                .path("steps")
                .list()
                .map { it.path("kind").asString() }
        assertFalse("signature" in kinds, kinds.toString())
        assertEquals(2, downloads(response).size)
        assertFalse(downloads(response).any { "minisig" in it })
        assertTrue(
            response.json
                .path("releaseKey")
                .path("id")
                .asString()
                .isNotEmpty(),
        )
    }
}
