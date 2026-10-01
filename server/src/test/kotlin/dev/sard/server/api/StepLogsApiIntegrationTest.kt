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
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** Rule "Логи шага — по порядку строк, усечение видно в ответе" (the HTTP-only scenarios). */
@RestApiTest
class StepLogsApiIntegrationTest(
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

    private class Step(
        val run: String,
        val id: String,
    )

    private fun step(name: String = "etc"): Step {
        val body = """{"name":"$name","agentId":"${agent.agentId}","plugin":"files","repositoryName":"qa","config":{"paths":["/etc"]}}"""
        val source =
            world.api
                .post("/api/v1/sources", admin, body)
                .json
                .path("id")
                .asString()
        val run =
            world.api
                .post("/api/v1/sources/$source/runs", admin, null)
                .json
                .path("id")
                .asString()
        val step = world.jdbc.queryForObject("select id from run_steps where run_id = ?", String::class.java, UUID.fromString(run))!!
        return Step(run, step)
    }

    private fun logs(
        step: Step,
        query: String = "",
    ) = world.api.get("/api/v1/runs/${step.run}/steps/${step.id}/logs$query", admin)

    @Test
    fun `Логи шага отдаются по порядку seq страницами`() {
        val step = step()
        world.insertLogs(UUID.fromString(step.id), tenant, 1200)

        val first = logs(step, "?afterSeq=0").json
        val last = logs(step, "?afterSeq=1000").json

        assertEquals((1..500).map { it.toLong() }, first.path("items").list().map { it.path("seq").asLong() })
        assertEquals(500, first.path("nextAfterSeq").asLong())
        assertTrue(first.path("hasMore").asBoolean())
        assertEquals((1001..1200).map { it.toLong() }, last.path("items").list().map { it.path("seq").asLong() })
        assertEquals(1200, last.path("nextAfterSeq").asLong())
        assertFalse(last.path("hasMore").asBoolean())
    }

    @Test
    fun `Пустая страница возвращает тот же afterSeq`() {
        val step = step()
        world.insertLogs(UUID.fromString(step.id), tenant, 10)

        val json = logs(step, "?afterSeq=10").json

        assertTrue(json.path("items").isEmpty)
        assertEquals(10, json.path("nextAfterSeq").asLong())
        assertFalse(json.path("hasMore").asBoolean())
    }

    @Test
    fun `Лог шага, по которому ничего не пришло, пуст`() {
        val json = logs(step()).json

        assertTrue(json.path("items").isEmpty)
        assertEquals(0, json.path("nextAfterSeq").asLong())
        assertFalse(json.path("hasMore").asBoolean())
    }

    @Test
    fun `Размер страницы логов на границах`() {
        val step = step()
        world.insertLogs(UUID.fromString(step.id), tenant, 1500)

        assertEquals(1, logs(step, "?limit=1").json.path("items").size())
        assertEquals(1000, logs(step, "?limit=1000").json.path("items").size())
    }

    @Test
    fun `Неверный параметр логов отклоняется`() {
        val step = step()
        for ((query, field) in listOf("?limit=0" to "limit", "?limit=1001" to "limit", "?afterSeq=-1" to "afterSeq")) {
            val response = logs(step, query)

            assertEquals(422, response.status, query)
            assertEquals("validation_failed", response.code)
            assertEquals(listOf(field), response.errorFields())
        }
    }

    @Test
    fun `Неусечённый лог отмечен truncated false`() {
        val step = step()
        world.insertLogs(UUID.fromString(step.id), tenant, 10)

        assertFalse(logs(step).json.path("truncated").asBoolean())
    }

    @Test
    fun `Шаг другого запуска не находится по пути этого запуска`() {
        val one = step("one")
        val two = step("two")

        val response = world.api.get("/api/v1/runs/${one.run}/steps/${two.id}/logs", admin)

        assertEquals(404, response.status)
        assertEquals("not_found", response.code)
    }
}
