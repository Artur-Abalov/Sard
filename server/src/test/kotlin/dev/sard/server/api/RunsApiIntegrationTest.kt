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
import java.util.UUID
import java.util.concurrent.Callable
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val RACE_SECONDS = 20L

/** Rules "Не больше одного активного запуска на источник" and "Удаление источника мягкое ... запрещено". */
@RestApiTest
class RunsApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired private val mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val agent = world.agent(tenant)

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun source(name: String = "etc"): String {
        val body = sourceJson(name, agent.agentId)
        return world.api
            .post("/api/v1/sources", admin, body)
            .json
            .path("id")
            .asString()
    }

    private fun start(
        source: String,
        session: ApiSession = admin,
    ) = world.api.post("/api/v1/sources/$source/runs", session, null)

    private fun delete(source: String) = world.api.send("DELETE", "/api/v1/sources/$source", admin)

    @Test
    fun `Запуск источника отвечает 201 с запуском в очереди`() {
        val source = source()

        val response = start(source)

        assertEquals(201, response.status)
        val json = response.json
        assertEquals(
            listOf("manual", "queued", T0.toString()),
            listOf("trigger", "status", "queuedAt").map {
                json.path(it).asString()
            },
        )
        assertEquals(source, json.path("sourceId").asString())
        assertEquals(agent.agentId.toString(), json.path("agentId").asString())
        val step = json.path("steps").single()
        assertEquals(
            listOf(0, "backup", "files", "qa", "queued"),
            listOf(
                step.path("ordinal").asInt(),
                step.path("action").asString(),
                step.path("plugin").asString(),
                step.path("repositoryName").asString(),
                step.path("status").asString(),
            ),
        )
    }

    @Test
    fun `Запуск при офлайн-агенте принимается и ждёт подключения`() {
        val response = start(source())

        assertEquals(201, response.status)
        assertEquals("queued", response.json.path("status").asString())
        Thread.sleep(QUIET_MILLIS)
        val run = world.api.get("/api/v1/runs/${response.json.path("id").asString()}", admin).json
        assertEquals("queued", run.path("status").asString())
    }

    @Test
    fun `Повторный запуск при активном отвечает 409 со ссылкой на активный`() {
        val source = source()
        val first = start(source).json.path("id").asString()

        val second = start(source)

        assertEquals(409, second.status)
        assertEquals("run_active", second.code)
        assertEquals(first, second.json.path("activeRunId").asString())
        assertEquals(1, world.count("runs", tenant))
    }

    @Test
    fun `Из десяти одновременных запусков создаётся ровно один`() {
        val source = source()
        val pool = Executors.newFixedThreadPool(10)
        val go = CountDownLatch(1)
        val results =
            (1..10).map {
                pool.submit(
                    Callable {
                        go.await()
                        start(source)
                    },
                )
            }
        go.countDown()
        val responses = results.map { it.get(RACE_SECONDS, TimeUnit.SECONDS) }
        pool.shutdown()

        val created = responses.single { it.status == 201 }
        val refused = responses.filter { it.status == 409 }
        assertEquals(9, refused.size)
        assertTrue(
            refused.all {
                it.code == "run_active" &&
                    it.json.path("activeRunId").asString() == created.json.path("id").asString()
            },
        )
        assertEquals(1, world.count("runs", tenant))
    }

    @Test
    fun `После завершения запуска источник можно запустить снова`() {
        val source = source()
        val first = start(source).json.path("id").asString()
        world.forceRun(UUID.fromString(first), "failed", "boom")

        val second = start(source)

        assertEquals(201, second.status)
        assertTrue(second.json.path("id").asString() != first)
    }

    @Test
    fun `Запуск удалённого источника отвечает 404`() {
        val source = source()
        delete(source)

        val response = start(source)

        assertEquals(404, response.status)
        assertEquals("not_found", response.code)
    }

    @Test
    fun `Запуск источника, устаревшего относительно снимка агента, отклоняется 409`() {
        for ((snapshot, code) in listOf(
            snapshotOf(plugins = emptyList()) to "unknown_plugin",
            snapshotOf(repositories = emptyList()) to "unknown_repository",
        )) {
            val source = source("etc-$code")
            world.register(agent, snapshot)

            val response = start(source)

            assertEquals(409, response.status, response.toString())
            assertEquals(code, response.code)
            world.register(agent, snapshotOf())
        }
        assertEquals(0, world.count("runs", tenant))
    }

    @Test
    fun `Запуск источника отозванного агента отклоняется 409 agent_revoked`() {
        val source = source()
        world.api.post("/api/v1/agents/${agent.agentId}/revoke", admin, null)

        val response = start(source)

        assertEquals(409, response.status)
        assertEquals("agent_revoked", response.code)
        assertEquals(0, world.count("runs", tenant))
    }

    @Test
    fun `Источник отозванного агента можно удалить`() {
        val source = source()
        world.api.post("/api/v1/agents/${agent.agentId}/revoke", admin, null)

        assertEquals(204, delete(source).status)
    }

    @Test
    fun `Удаление источника с активным запуском отвечает 409 со ссылкой на запуск`() {
        for (status in listOf("queued", "dispatched", "running")) {
            val source = source("etc-$status")
            val run = start(source).json.path("id").asString()
            world.forceRun(UUID.fromString(run), status)

            val response = delete(source)

            assertEquals(409, response.status, status)
            assertEquals("run_active", response.code)
            assertEquals(run, response.json.path("activeRunId").asString())
            assertEquals(200, world.api.get("/api/v1/sources/$source", admin).status)
            world.forceRun(UUID.fromString(run), "failed", "done")
        }
    }

    @Test
    fun `Одновременные удаление и запуск источника не оставляют запуска удалённого источника`() {
        val source = source()
        val pool = Executors.newFixedThreadPool(2)
        val go = CountDownLatch(1)
        val deletion =
            pool.submit(
                Callable {
                    go.await()
                    delete(source)
                },
            )
        val run =
            pool.submit(
                Callable {
                    go.await()
                    start(source)
                },
            )
        go.countDown()
        val deleted = deletion.get(RACE_SECONDS, TimeUnit.SECONDS)
        val started = run.get(RACE_SECONDS, TimeUnit.SECONDS)
        pool.shutdown()

        val outcome = listOf(deleted.status, started.status)
        assertTrue(outcome == listOf(204, 404) || (outcome == listOf(409, 201)), "$outcome")
        if (outcome == listOf(409, 201)) assertEquals("run_active", deleted.code)
    }

    @Test
    fun `Удаление источника сохраняет его запуски`() {
        val source = source()
        val run = start(source).json.path("id").asString()
        world.forceRun(UUID.fromString(run), "succeeded")

        delete(source)

        assertEquals(200, world.api.get("/api/v1/runs/$run", admin).status)
        assertEquals(
            listOf(run),
            world.api
                .get("/api/v1/runs?sourceId=$source", admin)
                .json
                .pluck("items", "id"),
        )
    }

    private companion object {
        const val QUIET_MILLIS = 300L
    }
}
