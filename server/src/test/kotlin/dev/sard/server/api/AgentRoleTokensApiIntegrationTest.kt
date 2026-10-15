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
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** V1, rule "Роль токена в REST" (docs/specs/server/agent-role.feature). */
@RestApiTest
class AgentRoleTokensApiIntegrationTest(
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
    private val admin = world.admin()

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun create(body: String) = world.api.post("/api/v1/enrollment-tokens", admin, body)

    private fun roleOfCard(id: String) =
        world.api
            .get("/api/v1/enrollment-tokens/$id", admin)
            .json
            .path("role")
            .asString()

    private fun roleInList(id: String) =
        world.api
            .get("/api/v1/enrollment-tokens", admin)
            .json
            .path("items")
            .first { it.path("id").asString() == id }
            .path("role")
            .asString()

    @Test
    fun `POST токена принимает роль и возвращает её`() {
        val cases =
            listOf(
                "{}" to "source",
                """{"role":"source"}""" to "source",
                """{"role":"verifier"}""" to "verifier",
                """{"role":null}""" to "source",
            )
        for ((body, role) in cases) {
            val response = create(body)
            assertEquals(201, response.status, body)
            val id = response.json.path("id").asString()
            assertEquals(role, roleOfCard(id), "card of $body")
            assertEquals(role, roleInList(id), "list of $body")
        }
    }

    @Test
    fun `Недопустимая роль токена отклоняется ошибкой у поля role`() {
        for (body in listOf(
            """{"role":""}""",
            """{"role":"SOURCE"}""",
            """{"role":"Verifier"}""",
            """{"role":"admin"}""",
            """{"role":1}""",
        )) {
            val response = create(body)
            assertEquals(422, response.status, body)
            assertEquals("validation_failed", response.code, body)
            assertEquals(listOf("role"), response.errorFields(), body)
        }
        assertEquals(0, world.count("enrollment_tokens", admin.tenant))
    }

    @Test
    fun `Недопустимая роль называется раньше срока и метки`() {
        val response = create("""{"ttlSeconds":1,"label":"${"x".repeat(201)}","role":"admin"}""")
        assertEquals(422, response.status)
        assertEquals(listOf("role"), response.errorFields())
        assertEquals(0, world.count("enrollment_tokens", admin.tenant))
    }

    @Test
    fun `Команда регистрации для токена verifier такая же, как для source`() {
        val source = create("{}").json
        val verifier = create("""{"role":"verifier"}""").json
        for (created in listOf(source, verifier)) {
            val token = created.path("token").asString()
            val command = created.path("enrollCommand").asString()
            assertTrue(command.startsWith("sudo sard-agent enroll --server "), command)
            assertTrue(command.endsWith(" --token $token"), command)
        }
        assertEquals(
            source.path("enrollCommand").asString().substringBefore(" --token "),
            verifier.path("enrollCommand").asString().substringBefore(" --token "),
        )
    }
}
