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

/** Rule "Срок задаётся у источника и в настройках установки": GET and PUT /api/v1/run-defaults. */
@RestApiTest
class RunDefaultsApiIntegrationTest(
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

    private fun save(
        body: String,
        session: ApiSession = admin,
    ) = world.api.send("PUT", "/api/v1/run-defaults", session, body)

    private fun read(session: ApiSession = admin) =
        world.api.get("/api/v1/run-defaults", session).json.let {
            it.path("backupTimeoutMinutes").asInt() to
                it.path("restoreTimeoutMinutes").asInt()
        }

    @Test
    fun `Новая установка отдаёт сроки по умолчанию 6 и 12 часов`() {
        val response = world.api.get("/api/v1/run-defaults", admin)

        assertEquals(200, response.status, response.toString())
        assertEquals(360 to 720, read())
    }

    @Test
    fun `Настройки установки сохраняют сроки по умолчанию`() {
        val response = save("""{"backupTimeoutMinutes":480,"restoreTimeoutMinutes":600}""")

        assertEquals(200, response.status, response.toString())
        val saved = response.json
        assertEquals(480, saved.path("backupTimeoutMinutes").asInt())
        assertEquals(600, saved.path("restoreTimeoutMinutes").asInt())
        assertEquals(480 to 600, read())
    }

    @Test
    fun `Недопустимые настройки установки отвечают 422 у поля и ничего не меняют`() {
        val bad =
            listOf(
                """{"backupTimeoutMinutes":null,"restoreTimeoutMinutes":600}""" to "backupTimeoutMinutes",
                """{"restoreTimeoutMinutes":600}""" to "backupTimeoutMinutes",
                """{"backupTimeoutMinutes":0,"restoreTimeoutMinutes":600}""" to "backupTimeoutMinutes",
                """{"backupTimeoutMinutes":480,"restoreTimeoutMinutes":1441}""" to "restoreTimeoutMinutes",
                """{"backupTimeoutMinutes":"6h","restoreTimeoutMinutes":600}""" to "backupTimeoutMinutes",
            )

        for ((body, field) in bad) {
            val response = save(body)
            assertEquals(422, response.status, "$body: $response")
            assertEquals("validation_failed", response.code)
            assertTrue(field in response.errorFields(), "$body: ${response.errorFields()} lacks $field")
        }
        assertEquals(360 to 720, read())
    }

    @Test
    fun `Настройки установки другого тенанта не видны`() {
        val other = world.admin()
        save("""{"backupTimeoutMinutes":60,"restoreTimeoutMinutes":90}""", other)

        assertEquals(360 to 720, read())
        assertEquals(60 to 90, read(other))
    }

    @Test
    fun `Без сессии настройки установки отвечают 401`() {
        assertEquals(401, world.api.get("/api/v1/run-defaults", null).status)
    }
}
