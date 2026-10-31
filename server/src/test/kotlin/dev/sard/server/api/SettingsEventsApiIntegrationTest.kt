// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.extension.EventLog
import dev.sard.server.extension.FixturePolicy
import dev.sard.server.extension.FixturePolicyConfiguration
import dev.sard.server.extension.RecordingConfiguration
import dev.sard.server.extension.SettingsChanged
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
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val BAD_SCHEDULE = """{"cron":"not a cron","timezone":"UTC","enabled":true}"""
private const val SCHEDULE = """{"cron":"0 4 * * *","timezone":"UTC","enabled":true}"""

/** Rule "Изменения настроек публикуют событие с видом настройки" (server-core-extensions.feature). */
@RestApiTest
@Import(RecordingConfiguration::class, FixturePolicyConfiguration::class)
class SettingsEventsApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @Autowired private val log: EventLog,
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
    fun `no events yet`() {
        policy.reset()
        log.clear()
    }

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

    private fun only(setting: String): SettingsChanged {
        val found = log.settings(setting)
        assertEquals(1, found.size, "$setting: ${log.events}")
        assertEquals(Subject.ADMINISTRATOR, found.single().actor)
        assertEquals(tenant, found.single().tenantId)
        return found.single()
    }

    private fun defaults(
        backup: Int,
        restore: Int,
    ) = """{"backupTimeoutMinutes":$backup,"restoreTimeoutMinutes":$restore}"""

    private fun ok(response: ApiResponse) = assertTrue(response.status in 200..299, response.toString())

    @Test
    fun `Изменение сроков по умолчанию даёт событие run_defaults`() {
        ok(api.send("PUT", "/api/v1/run-defaults", admin, defaults(481, 601)))

        assertNull(only("run_defaults").objectId)
    }

    @Test
    fun `Создание источника даёт событие source_created с id источника`() {
        val id = source()

        assertEquals(id, only("source_created").objectId)
    }

    @Test
    fun `Изменение источника даёт событие source_updated`() {
        val id = source()
        log.clear()

        ok(api.send("PUT", "/api/v1/sources/$id", admin, sourceJson("renamed-${UUID.randomUUID()}", agent.agentId)))

        assertEquals(id, only("source_updated").objectId)
    }

    @Test
    fun `Удаление источника даёт событие source_deleted`() {
        val id = source()
        log.clear()

        ok(api.send("DELETE", "/api/v1/sources/$id", admin))

        assertEquals(id, only("source_deleted").objectId)
    }

    @Test
    fun `Расписание источника даёт событие source_schedule`() {
        val id = source()
        log.clear()

        ok(api.send("PUT", "/api/v1/sources/$id/schedule", admin, SCHEDULE))

        assertEquals(id, only("source_schedule").objectId)
    }

    @Test
    fun `Проверка источника даёт событие source_verification`() {
        val id = source()
        log.clear()

        ok(api.send("PUT", "/api/v1/sources/$id/verification", admin, """{"enabled":false}"""))

        assertEquals(id, only("source_verification").objectId)
    }

    @Test
    fun `Расписание проверок даёт событие verification_schedule`() {
        ok(api.send("PUT", "/api/v1/verification-schedule", admin, """{"cron":"15 3 * * *","timezone":"UTC"}"""))

        assertNull(only("verification_schedule").objectId)
    }

    @Test
    fun `Токен регистрации даёт события token_created и token_revoked`() {
        val created = api.send("POST", "/api/v1/enrollment-tokens", admin, "{}")
        ok(created)
        val id = UUID.fromString(created.json.path("id").asString())
        assertEquals(id, only("token_created").objectId)
        log.clear()

        ok(api.send("POST", "/api/v1/enrollment-tokens/$id/revoke", admin, null))

        assertEquals(id, only("token_revoked").objectId)
    }

    @Test
    fun `Отзыв агента даёт событие agent_revoked`() {
        val other = world.agent(tenant, hostname = "to-revoke")

        ok(api.send("POST", "/api/v1/agents/${other.agentId}/revoke", admin, null))

        assertEquals(other.agentId, only("agent_revoked").objectId)
    }

    @Test
    fun `Повтор без изменений не даёт события`() {
        val id = source()
        val repeated =
            listOf(
                Triple("PUT", "/api/v1/run-defaults", """{"backupTimeoutMinutes":482,"restoreTimeoutMinutes":602}"""),
                Triple("PUT", "/api/v1/sources/$id/schedule", SCHEDULE),
                Triple("PUT", "/api/v1/sources/$id/verification", """{"enabled":false}"""),
                Triple("PUT", "/api/v1/verification-schedule", """{"cron":"16 3 * * *","timezone":"UTC"}"""),
            )
        for ((method, path, body) in repeated) ok(api.send(method, path, admin, body))
        val body = sourceJson("same", agent.agentId)
        ok(api.send("PUT", "/api/v1/sources/$id", admin, body))
        val token =
            api
                .send("POST", "/api/v1/enrollment-tokens", admin, "{}")
                .json
                .path("id")
                .asString()
        ok(api.send("POST", "/api/v1/enrollment-tokens/$token/revoke", admin, null))
        val other = world.agent(tenant, hostname = "twice")
        ok(api.send("POST", "/api/v1/agents/${other.agentId}/revoke", admin, null))
        log.clear()

        for ((method, path, repeatBody) in repeated) ok(api.send(method, path, admin, repeatBody))
        ok(api.send("PUT", "/api/v1/sources/$id", admin, body))
        ok(api.send("POST", "/api/v1/enrollment-tokens/$token/revoke", admin, null))
        ok(api.send("POST", "/api/v1/agents/${other.agentId}/revoke", admin, null))

        assertEquals(emptyList(), log.of<SettingsChanged>())
    }

    @Test
    fun `Отклонённое проверкой изменение не даёт события`() {
        val id = source()
        log.clear()

        val response = api.send("PUT", "/api/v1/sources/$id/schedule", admin, BAD_SCHEDULE)

        assertEquals(422, response.status)
        assertEquals(emptyList(), log.of<SettingsChanged>())
    }

    @Test
    fun `Чтение настроек не даёт события`() {
        api.get("/api/v1/run-defaults", admin)
        api.get("/api/v1/sources", admin)

        assertEquals(emptyList(), log.of<SettingsChanged>())
    }

    @Test
    fun `Запрещённое изменение настроек не даёт события`() {
        policy.denied = setOf("PUT /api/v1/run-defaults")

        val response = api.send("PUT", "/api/v1/run-defaults", admin, defaults(483, 603))

        assertEquals(403, response.status)
        assertEquals(emptyList(), log.of<SettingsChanged>())
    }
}
