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
import java.time.Duration
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** Rule "Список и карточка запусков показывают шаги с причиной ошибки как есть" (the HTTP-only scenarios). */
@RestApiTest
class RunsReadApiIntegrationTest(
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
    private val clock = world.clock
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val agent = world.agent(tenant)

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun source(
        name: String = "etc",
        on: UUID = agent.agentId,
    ): String {
        val body = sourceJson(name, on)
        return world.api
            .post("/api/v1/sources", admin, body)
            .json
            .path("id")
            .asString()
    }

    private fun start(source: String): String =
        world.api
            .post("/api/v1/sources/$source/runs", admin, null)
            .json
            .path("id")
            .asString()

    private fun ids(query: String) =
        world.api
            .get("/api/v1/runs$query", admin)
            .json
            .pluck("items", "id")

    @Test
    fun `Список запусков фильтруется по источнику`() {
        val s1 = source("s1")
        val s2 = source("s2")
        val a = start(s1).also { world.forceRun(UUID.fromString(it), "succeeded") }
        val b = start(s1)
        start(s2)
        start(source("s3"))

        assertEquals(setOf(a, b), ids("?sourceId=$s1").toSet())
    }

    @Test
    fun `Список запусков фильтруется по агенту`() {
        val other = world.agent(tenant)
        val x = start(source("x"))
        start(source("y", other.agentId))

        assertEquals(listOf(x), ids("?agentId=${agent.agentId}"))
    }

    @Test
    fun `Несколько статусов в фильтре объединяются по ИЛИ`() {
        val statuses = listOf("queued", "running", "succeeded", "failed")
        val byStatus =
            statuses.associateWith { status ->
                start(source(status)).also {
                    world.forceRun(
                        UUID.fromString(it),
                        status,
                        "m".takeIf {
                            status ==
                                "failed"
                        },
                    )
                }
            }

        val found = ids("?status=failed&status=succeeded").toSet()

        assertEquals(setOf(byStatus.getValue("failed"), byStatus.getValue("succeeded")), found)
    }

    @Test
    fun `Разные фильтры объединяются по И`() {
        val s = source("s")
        val t = source("t")
        start(s).also { world.forceRun(UUID.fromString(it), "succeeded") }
        val failed = start(s).also { world.forceRun(UUID.fromString(it), "failed", "boom") }
        start(t).also { world.forceRun(UUID.fromString(it), "failed", "boom") }

        assertEquals(listOf(failed), ids("?sourceId=$s&status=failed"))
    }

    @Test
    fun `Список запусков фильтруется по времени постановки в очередь`() {
        val source = source()
        val runs =
            (0..2).map { hour ->
                clock.now = T0.plus(Duration.ofHours(hour.toLong()))
                start(source).also { world.forceRun(UUID.fromString(it), "succeeded") }
            }

        val found = ids("?queuedFrom=${T0.plus(Duration.ofHours(1))}&queuedTo=${T0.plus(Duration.ofHours(2))}")

        assertEquals(listOf(runs[1]), found)
    }

    @Test
    fun `Начало интервала позже конца отклоняется`() {
        val response = world.api.get("/api/v1/runs?queuedFrom=${T0.plusSeconds(3600)}&queuedTo=$T0", admin)

        assertEquals(422, response.status)
        assertEquals("validation_failed", response.code)
        assertEquals(listOf("queuedFrom"), response.errorFields())
    }

    @Test
    fun `Запуски удалённого источника остаются в списке`() {
        val s = source()
        val run = start(s).also { world.forceRun(UUID.fromString(it), "succeeded") }
        world.api.send("DELETE", "/api/v1/sources/$s", admin)

        assertEquals(listOf(run), ids(""))
    }

    @Test
    fun `Элемент списка запусков не содержит шагов, карточка содержит`() {
        val run = start(source())

        val item =
            world.api
                .get("/api/v1/runs", admin)
                .json
                .path("items")
                .get(0)
        val card = world.api.get("/api/v1/runs/$run", admin).json

        assertTrue(item.path("steps").isMissingNode)
        assertEquals(1, card.path("steps").size())
        val step = card.path("steps").get(0)
        assertEquals(
            world.jdbc.queryForObject(
                "select id from run_steps where run_id = ?",
                String::class.java,
                UUID.fromString(run),
            ),
            step.path("id").asString(),
        )
    }

    @Test
    fun `Запуск показывает агента, который его выполнял, после переноса источника`() {
        val other = world.agent(tenant)
        val s = source()
        val run = start(s).also { world.forceRun(UUID.fromString(it), "succeeded") }
        val body = sourceJson("etc", other.agentId)
        assertEquals(200, world.api.send("PUT", "/api/v1/sources/$s", admin, body).status)

        val card = world.api.get("/api/v1/runs/$run", admin).json

        assertEquals(agent.agentId.toString(), card.path("agentId").asString())
        assertEquals(
            agent.agentId.toString(),
            card
                .path("steps")
                .get(0)
                .path("agentId")
                .asString(),
        )
        assertEquals(listOf(run), ids("?agentId=${agent.agentId}"))
        assertEquals(emptyList(), ids("?agentId=${other.agentId}"))
    }

    @Test
    fun `Карточка несуществующего запуска отвечает 404`() {
        val response = world.api.get("/api/v1/runs/${UUID.randomUUID()}", admin)

        assertEquals(404, response.status)
        assertEquals("not_found", response.code)
    }

    @Test
    fun `Причина ошибки шага показана как есть`() {
        val cases =
            listOf(
                "failed" to "restic exited 1",
                "rejected" to "unknown plugin files",
                "timed_out" to "deadline exceeded",
                "lost" to "agent lost the step",
            )
        for ((status, message) in cases) {
            val run = start(source("etc-$status")).also { world.forceRun(UUID.fromString(it), status, message) }

            val card = world.api.get("/api/v1/runs/$run", admin).json

            assertEquals("failed", card.path("status").asString())
            assertEquals(
                status,
                card
                    .path("steps")
                    .get(0)
                    .path("status")
                    .asString(),
            )
            assertEquals(message, card.path("message").asString())
            assertEquals(
                message,
                card
                    .path("steps")
                    .get(0)
                    .path("message")
                    .asString(),
            )
        }
    }
}
