// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import ch.qos.logback.classic.Level
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.onboarding.OnboardingSteps
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import dev.sard.server.selfagent.captureEvents
import org.mockito.Mockito
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.dao.DataAccessResourceFailureException
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean
import tools.jackson.databind.ObjectMapper
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val EVIL = "https://evil.example"
private const val SCHEDULE = """{"cron":"30 1 * * *","timezone":"UTC","enabled":true}"""
private val TABLES = listOf("enrollment_tokens", "sources", "runs", "schedules", "agents", "self_backups")

/**
 * Rule "Пока онбординг не завершён, изменяющие действия отклоняются (D8)" and the rule of the upgraded installation
 * of docs/specs/server/onboarding-self-backup.feature (@http).
 */
@RestApiTest
class OnboardingGuardApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    @MockitoSpyBean
    lateinit var steps: OnboardingSteps

    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)

    // Enrolled while the onboarding is complete, as the tests of the other features do: an ordinary agent
    // cannot enroll before the end of the onboarding.
    private val agent = world.agent(tenant)

    @BeforeTest
    fun `the onboarding is not complete`() = jdbc.startOnboarding()

    @AfterTest
    fun `the onboarding is complete again, drop the tenants`() {
        Mockito.reset(steps)
        jdbc.finishOnboarding()
        world.close()
    }

    private fun counts(): Map<String, Int?> =
        TABLES.associateWith {
            jdbc.queryForObject("select count(*) from $it where tenant_id = ?", Int::class.java, tenant)
        }

    private fun send(
        method: String,
        path: String,
        body: String? = null,
        headers: Map<String, String> = emptyMap(),
        session: ApiSession? = admin,
    ) = world.api.send(method, path, session, body, headers)

    private fun validSource(): String = sourceJson("guarded", agent.agentId)

    // ---- Правило: Пока онбординг не завершён, изменяющие действия отклоняются (D8) ----

    @Test
    fun `Изменяющий запрос вне разрешённых отвечает onboarding_incomplete и ничего не меняет`() {
        val source = validSource()
        val random = UUID.randomUUID()
        val requests =
            listOf(
                Triple("POST", "/api/v1/enrollment-tokens", "{}"),
                Triple("POST", "/api/v1/enrollment-tokens/$random/revoke", null),
                Triple("POST", "/api/v1/sources", source),
                Triple("PUT", "/api/v1/sources/$random", source),
                Triple("DELETE", "/api/v1/sources/$random", null),
                Triple("POST", "/api/v1/sources/$random/runs", null),
                Triple("PUT", "/api/v1/sources/$random/schedule", SCHEDULE),
                Triple("POST", "/api/v1/agents/$random/revoke", null),
            )
        val before = counts()

        for ((method, path, body) in requests) {
            val response = send(method, path, body)

            assertEquals(409, response.status, "$method $path: $response")
            assertEquals("onboarding_incomplete", response.code, "$method $path")
        }
        assertEquals(before, counts())
    }

    @Test
    fun `Чтение не ограничено до завершения онбординга`() {
        val paths =
            listOf(
                "/api/v1/agents",
                "/api/v1/sources",
                "/api/v1/runs",
                "/api/v1/overview",
                "/api/v1/ca",
                "/api/v1/enrollment-tokens",
                "/api/v1/self-backup",
                "/api/v1/agent-install",
                "/api/v1/onboarding",
            )
        for (path in paths) assertEquals(200, send("GET", path).status, path)
    }

    @Test
    fun `Разрешённые изменяющие запросы не отвечают onboarding_incomplete`() {
        world.selfAgent(tenant)
        val bound = send("PUT", "/api/v1/self-backup/repository", """{"repositoryName":"self"}""")
        assertEquals(200, bound.status, bound.toString())
        val system =
            bound.json
                .path("sources")
                .path(1)
                .path("sourceId")
                .asString()

        val runs = send("POST", "/api/v1/self-backup/runs", "")
        val schedule = send("PUT", "/api/v1/sources/$system/schedule", SCHEDULE)
        val password =
            send(
                "PUT",
                "/api/v1/session/password",
                """{"currentPassword":"$TEST_ADMIN_PASSWORD","newPassword":"$TEST_ADMIN_PASSWORD"}""",
            )
        val signedOut = send("DELETE", "/api/v1/session", null, session = world.api.signIn(tenant))

        val statuses = listOf(runs.status, schedule.status, password.status, signedOut.status)
        assertEquals(listOf(200, 200, 204, 204), statuses)
    }

    @Test
    fun `Отзыв встроенного агента с подтверждением разрешён до завершения онбординга`() {
        val builtin = world.selfAgent(tenant)

        val revoked = send("POST", "/api/v1/agents/${builtin.agentId}/revoke?confirm=sard-self")

        assertEquals(200, revoked.status, revoked.toString())
        assertEquals(T0.toString(), revoked.json.path("revokedAt").asString())
    }

    @Test
    fun `Отзыв обычного агента до завершения онбординга отвечает onboarding_incomplete`() {
        val refused = send("POST", "/api/v1/agents/${agent.agentId}/revoke")

        assertEquals("onboarding_incomplete", refused.code)
    }

    @Test
    fun `Расписание обычного источника до завершения онбординга отвечает onboarding_incomplete`() {
        jdbc.finishOnboarding()
        val created = send("POST", "/api/v1/sources", sourceJson("mine", agent.agentId))
        assertEquals(201, created.status, created.toString())
        jdbc.startOnboarding()

        val refused = send("PUT", "/api/v1/sources/${created.json.path("id").asString()}/schedule", SCHEDULE)

        assertEquals(409, refused.status, refused.toString())
        assertEquals("onboarding_incomplete", refused.code)
    }

    @Test
    fun `Вход администратора разрешён до завершения онбординга`() {
        val signedIn = world.api.signIn(tenant)

        assertTrue(signedIn.cookie.isNotEmpty())
    }

    @Test
    fun `Изменяющий запрос без сессии отвечает 401, а не onboarding_incomplete`() {
        val response = send("POST", "/api/v1/enrollment-tokens", "{}", session = null)

        assertEquals(401, response.status)
        assertEquals("unauthenticated", response.code)
    }

    @Test
    fun `Изменяющий запрос с чужим Origin отвечает 403, а не onboarding_incomplete`() {
        val response = send("POST", "/api/v1/enrollment-tokens", "{}", mapOf("Origin" to EVIL))

        assertEquals(403, response.status)
        assertEquals("origin_rejected", response.code)
    }

    @Test
    fun `Ограничение проверяется раньше тела запроса`() {
        val response = send("POST", "/api/v1/sources", "not json")

        assertEquals(409, response.status)
        assertEquals("onboarding_incomplete", response.code)
    }

    @Test
    fun `При недоступной базе изменяющий запрос отвечает 503, а не onboarding_incomplete`() {
        Mockito.doThrow(DataAccessResourceFailureException("down")).`when`(steps).keysConfirmed()

        val response = send("POST", "/api/v1/enrollment-tokens", "{}")

        assertEquals(503, response.status, response.toString())
        assertEquals("unavailable", response.code)
    }

    @Test
    fun `После завершения онбординга выпуск токена регистрации проходит`() {
        jdbc.finishOnboarding()

        val response = send("POST", "/api/v1/enrollment-tokens", "{}")

        assertEquals(201, response.status, response.toString())
        assertTrue(
            response.json
                .path("token")
                .asString()
                .isNotEmpty(),
        )
    }

    @Test
    fun `Отказ onboarding_incomplete не пишет строку журнала на каждый запрос`() {
        val events = captureEvents { repeat(10) { send("POST", "/api/v1/enrollment-tokens", "{}") } }

        val loud = events.filter { it.level.isGreaterOrEqual(Level.INFO) }
        assertTrue(loud.none { "onboarding" in it.text.lowercase() || "enrollment" in it.text.lowercase() }, "$loud")
    }

    // ---- Правило: Обновлённая установка блокирует только регистрацию новых агентов ----

    @Test
    fun `Обновлённая установка не выпускает токены регистрации до завершения онбординга`() {
        jdbc.markUpgraded()

        val response = send("POST", "/api/v1/enrollment-tokens", "{}")

        assertEquals(409, response.status)
        assertEquals("onboarding_incomplete", response.code)
        assertEquals("new_agents", send("GET", "/api/v1/onboarding").json.path("restriction").asString())
    }

    @Test
    fun `Обновлённая установка не выпускает токен и по обходному пути`() {
        jdbc.markUpgraded()
        val before = counts()

        val paths = listOf("/api/v1/enrollment-tokens;x=1", "/api/v1/enrollment%2Dtokens", "/api/v1/enrollment-tokens/")
        for (path in paths) {
            val response = world.api.sendUnchecked("POST", path, admin, "{}")

            assertTrue(response.status != 201, "$path: $response")
            assertTrue(response.status in setOf(400, 404, 409), "$path: $response")
            if (response.status == 409) assertEquals("onboarding_incomplete", response.code, path)
        }
        assertEquals(before, counts())
    }

    @Test
    fun `Полное ограничение не открывает запрещённое действие обходным путём`() {
        val source = validSource()
        val random = UUID.randomUUID()
        val requests =
            listOf(
                "/api/v1/onboarding/../sources",
                "/api/v1/sources;x=1",
                "/api/v1/s%6Frces",
                "/api/v1/enrollment-tokens;x=1",
                "/api/v1/onboarding;x=1/../sources",
                "/api/v1/sources/$random;x=1/schedule",
                "/api/v1/agents/$random%2Frevoke",
            )
        val before = counts()

        for (path in requests) {
            val response = world.api.sendUnchecked("POST", path, admin, source)

            assertTrue(response.status != 201 && response.status != 200, "$path: $response")
            if (response.status == 409) assertEquals("onboarding_incomplete", response.code, path)
        }
        assertEquals(before, counts())
    }

    @Test
    fun `Обновлённая установка разрешает прочие изменения до завершения онбординга`() {
        jdbc.markUpgraded()
        val created = send("POST", "/api/v1/sources", sourceJson("mine", agent.agentId))
        assertEquals(201, created.status, created.toString())
        val source = created.json.path("id").asString()

        val cron = """{"cron":"0 4 * * *","timezone":"UTC","enabled":true}"""
        val schedule = send("PUT", "/api/v1/sources/$source/schedule", cron)
        val run = send("POST", "/api/v1/sources/$source/runs", null)
        val revoked = send("POST", "/api/v1/agents/${agent.agentId}/revoke")

        // A run is created: 201, as for every start of a run (the specification's table says 200).
        assertEquals(listOf(200, 201, 200), listOf(schedule.status, run.status, revoked.status))
    }
}
