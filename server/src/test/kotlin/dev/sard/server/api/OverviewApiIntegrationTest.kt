// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.agents.stream.TestAgent
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import dev.sard.server.registration.RepositoryEntry
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.time.Duration
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals

private val STEPS = listOf("tokenIssued", "agentConnected", "repositoryInitialized", "sourceCreated", "backupSucceeded")

/** Rules "Обзор считает агентов тенанта..." and "Каждый первый шаг выполнен по своему правилу сервера" (W2 К14). */
@RestApiTest
class OverviewApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired private val mapper: ObjectMapper,
    @Autowired private val hellos: ReconciledHellos,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val clock = world.clock
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun overview(session: ApiSession = admin): JsonNode = world.api.get("/api/v1/overview", session).json

    private fun steps(json: JsonNode = overview()): Map<String, Boolean> =
        (STEPS + "complete").associateWith {
            val mark = json.path("firstSteps").path(it)
            assertEquals(true, mark.isBoolean, "$it is not a boolean: $mark")
            mark.asBoolean()
        }

    private fun counts(json: JsonNode = overview()): Pair<Int, Int> {
        val (online, total) = listOf("agentsOnline", "agentsTotal").map { json.path(it).asInt() }
        return online to total
    }

    private fun online(agent: TestAgent) =
        world.connect(agent).also {
            it.hello()
            hellos.await(agent.agentId)
        }

    private fun source(
        agent: TestAgent,
        name: String = "etc",
    ): String =
        world.api
            .post("/api/v1/sources", admin, sourceJson(name, agent.agentId))
            .json
            .path("id")
            .asString()

    private fun start(source: String): UUID =
        UUID.fromString(
            world.api
                .post("/api/v1/sources/$source/runs", admin, null)
                .json
                .path("id")
                .asString(),
        )

    private fun initializedRepository() = RepositoryEntry("qa", "local", "a0".padEnd(64, '0'), "")

    private fun revoke(agent: TestAgent) = world.api.post("/api/v1/agents/${agent.agentId}/revoke", admin, null)

    @Test
    fun `Пустой тенант даёт нулевые счётчики и невыполненные шаги`() {
        val json = overview()

        assertEquals(0 to 0, counts(json))
        assertEquals(STEPS.map { it to false } + ("complete" to false), steps(json).toList())
    }

    @Test
    fun `Обзор считает онлайн и всех неотозванных агентов`() {
        val x = world.agent(tenant)
        world.agent(tenant)
        world.agent(tenant)
        val w = world.agent(tenant)
        online(x)
        revoke(w)

        assertEquals(1 to 3, counts())
    }

    @Test
    fun `Счётчик онлайн совпадает со статусом в списке агентов`() {
        val x = world.agent(tenant)
        val y = world.agent(tenant)
        online(y)
        clock.now = T0.plusSeconds(91)
        online(x)

        val listed =
            world.api
                .get("/api/v1/agents?status=online", admin)
                .json
                .path("items")
                .size()

        assertEquals(1, listed)
        assertEquals(listed, counts().first)
    }

    @Test
    fun `Обзор не содержит агентов другого тенанта`() {
        val other = world.tenant()
        online(world.agent(other))
        world.agent(other)

        val json = overview(world.admin(tenant))

        assertEquals(0 to 0, counts(json))
        assertEquals(false, steps(json).getValue("complete"))
        assertEquals(setOf(false), steps(json).values.toSet())
    }

    @Test
    fun `Обзор без сессии отвечает 401`() {
        val response = world.api.get("/api/v1/overview", null)

        assertEquals(401, response.status)
        assertEquals("unauthenticated", response.code)
    }

    @Test
    fun `Токен в любом статусе отмечает выпуск токена`() {
        val active = world.tenant().let { world.admin(it) }.also { world.api.post("/api/v1/enrollment-tokens", it) }
        val revoked =
            world.admin(world.tenant()).also {
                val id =
                    world.api
                        .post("/api/v1/enrollment-tokens", it)
                        .json
                        .path("id")
                        .asString()
                world.api.post("/api/v1/enrollment-tokens/$id/revoke", it, null)
            }
        val expired =
            world.admin(world.tenant()).also {
                world.api.post("/api/v1/enrollment-tokens", it, """{"ttlSeconds":300}""")
            }
        val used = world.admin(world.tenant().also { world.enroll(it) })
        clock.now = T0.plus(Duration.ofMinutes(6))

        for (session in listOf(active, revoked, expired, used)) {
            assertEquals(true, steps(overview(session)).getValue("tokenIssued"))
        }
        assertEquals(false, steps(overview(session = world.admin(world.tenant()))).getValue("tokenIssued"))
    }

    @Test
    fun `Зарегистрированный, но ни разу не подключавшийся агент не отмечает подключение`() {
        world.agent(tenant)

        assertEquals(false, steps().getValue("agentConnected"))
    }

    @Test
    fun `Агент, приславший Hello, отмечает подключение и после ухода в офлайн`() {
        online(world.agent(tenant))
        clock.now = T0.plusSeconds(91)

        assertEquals(0 to 1, counts())
        assertEquals(true, steps().getValue("agentConnected"))
    }

    @Test
    fun `Отзыв единственного подключавшегося агента снимает отметку подключения`() {
        val agent = world.agent(tenant)
        online(agent)
        assertEquals(true, steps().getValue("agentConnected"))

        revoke(agent)

        val after = steps()
        assertEquals(false, after.getValue("agentConnected"))
        assertEquals(false, after.getValue("repositoryInitialized"))
    }

    @Test
    fun `Инициализированный репозиторий определяется по последнему Register`() {
        val initialized = RepositoryEntry("qa", "local", "a0".padEnd(64, '0'), "")
        val uninitialized = RepositoryEntry("qa", "local", "", "")
        val agent = world.agent(tenant, snapshotOf(repositories = emptyList()))
        val cases =
            listOf(
                listOf(initialized) to true,
                listOf(uninitialized) to false,
                emptyList<RepositoryEntry>() to false,
            )
        for ((repositories, expected) in cases) {
            world.register(agent, snapshotOf(repositories = repositories))
            assertEquals(expected, steps().getValue("repositoryInitialized"), repositories.toString())
        }
        world.register(agent, snapshotOf(repositories = listOf(initialized)))
        world.register(agent, snapshotOf(repositories = listOf(uninitialized)))
        assertEquals(false, steps().getValue("repositoryInitialized"))
    }

    @Test
    fun `Удаление единственного источника снимает отметку создания источника`() {
        val s = source(world.agent(tenant))
        assertEquals(true, steps().getValue("sourceCreated"))

        world.api.send("DELETE", "/api/v1/sources/$s", admin)

        assertEquals(false, steps().getValue("sourceCreated"))
    }

    @Test
    fun `Успешный запуск отмечает первый бэкап и после удаления источника`() {
        val s = source(world.agent(tenant))
        world.forceRun(start(s), "succeeded")
        world.api.send("DELETE", "/api/v1/sources/$s", admin)

        assertEquals(true, steps().getValue("backupSucceeded"))
    }

    @Test
    fun `Только успешный запуск отмечает первый бэкап`() {
        val agent = world.agent(tenant)
        for (status in listOf("failed", "lost", "rejected", "running")) {
            world.forceRun(start(source(agent, status)), status, "m".takeIf { status != "running" })

            assertEquals(false, steps().getValue("backupSucceeded"), status)
        }

        world.forceRun(start(source(agent, "ok")), "succeeded")

        assertEquals(true, steps().getValue("backupSucceeded"))
    }

    @Test
    fun `Неуспешный шаг со снимком не отмечает первый бэкап`() {
        val agent = world.agent(tenant)
        val run = start(source(agent))
        world.forceRun(run, "failed", "2 files unreadable")
        val step = world.jdbc.queryForObject("select id from run_steps where run_id = ?", UUID::class.java, run)!!
        world.insertSnapshot(tenant, step, "a1b2c3")

        assertEquals(false, steps().getValue("backupSucceeded"))
    }

    @Test
    fun `Все пять шагов дают complete true`() {
        val agent = world.agent(tenant, snapshotOf(repositories = listOf(initializedRepository())))
        online(agent)
        world.api.post("/api/v1/enrollment-tokens", admin)
        world.forceRun(start(source(agent)), "succeeded")

        assertEquals(STEPS.map { it to true } + ("complete" to true), steps().toList())
    }

    @Test
    fun `Четыре шага из пяти дают complete false`() {
        val agent = world.agent(tenant, snapshotOf(repositories = listOf(initializedRepository())))
        online(agent)
        world.api.post("/api/v1/enrollment-tokens", admin)

        val result = steps()

        assertEquals(false, result.getValue("complete"))
        assertEquals(listOf("sourceCreated", "backupSucceeded"), result.filterValues { !it }.keys.toList() - "complete")
    }
}
