// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import dev.sard.server.scheduler.Scheduler
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.time.temporal.ChronoUnit
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** F3a REST: a source's schedule (set, read, disable) and the journal of its fires. */
@RestApiTest
class SchedulesApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired private val mapper: ObjectMapper,
    @Autowired private val scheduler: Scheduler,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val clock = world.clock
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val agent = world.agent(tenant)
    private val nextHour = T0.truncatedTo(ChronoUnit.HOURS).plus(1, ChronoUnit.HOURS)

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun source(): String {
        val body =
            """{"name":"etc","agentId":"${agent.agentId}","plugin":"files","repositoryName":"qa",""" +
                """"config":{"paths":["/etc"]}}"""
        return world.api
            .post("/api/v1/sources", admin, body)
            .json
            .path("id")
            .asString()
    }

    private fun put(
        source: String,
        cron: String = "0 * * * *",
        timezone: String = "UTC",
        enabled: Boolean = true,
    ) = world.api.send(
        "PUT",
        "/api/v1/sources/$source/schedule",
        admin,
        """{"cron":"$cron","timezone":"$timezone","enabled":$enabled}""",
    )

    @Test
    fun `a schedule is set on a source, read back, and disabled`() {
        val source = source()

        val set = put(source, cron = " 0  *  * * * ")

        assertEquals(200, set.status, set.toString())
        val json = set.json
        assertEquals(source, json.path("sourceId").asString())
        assertEquals(listOf("0 * * * *", "UTC"), listOf("cron", "timezone").map { json.path(it).asString() })
        assertEquals(true, json.path("enabled").asBoolean())
        assertEquals(nextHour.toString(), json.path("nextRunAt").asString())
        assertEquals(0, json.path("skippedInRow").asInt())
        assertEquals(json, world.api.get("/api/v1/sources/$source/schedule", admin).json)

        val disabled = put(source, enabled = false).json
        assertEquals(false, disabled.path("enabled").asBoolean())
        assertTrue(disabled.path("nextRunAt").isNull)
        assertEquals(json.path("id"), disabled.path("id"))
    }

    @Test
    fun `a wrong cron or zone is refused naming the field`() {
        val source = source()
        val wrong = listOf(Triple("0 * * *", "UTC", "cron"), Triple("0 * * * *", "Mars/Olympus", "timezone"))
        for ((cron, zone, field) in wrong) {
            val response = put(source, cron = cron, timezone = zone)
            assertEquals(422, response.status, response.toString())
            assertEquals("validation_failed", response.code)
            assertEquals(listOf(field), response.errorFields())
        }
    }

    @Test
    fun `a source without a schedule, or no source at all, answers 404`() {
        val source = source()
        assertEquals(404, world.api.get("/api/v1/sources/$source/schedule", admin).status)
        assertEquals(404, world.api.get("/api/v1/sources/${UUID.randomUUID()}/schedule", admin).status)
        assertEquals(404, put(UUID.randomUUID().toString()).status)
        assertEquals(404, world.api.get("/api/v1/sources/${UUID.randomUUID()}/schedule/fires", admin).status)
    }

    @Test
    fun `the journal lists fires newest first, a page at a time`() {
        val source = source()
        assertEquals(
            0,
            world.api
                .get("/api/v1/sources/$source/schedule/fires", admin)
                .json
                .path("items")
                .size(),
        )
        put(source)
        clock.now = nextHour
        scheduler.tick()
        clock.now = nextHour.plus(1, ChronoUnit.HOURS)
        scheduler.tick()

        val all = world.api.get("/api/v1/sources/$source/schedule/fires", admin).json
        assertEquals(listOf("skipped_active", "run_created"), all.pluck("items", "outcome"))
        val created = all.path("items").get(1)
        assertEquals(
            listOf("schedule", nextHour.toString()),
            listOf(created.path("kind").asString(), created.path("scheduledFor").asString()),
        )
        assertEquals(all.path("items").get(0).path("runId"), created.path("runId"))
        assertEquals(
            1,
            all
                .path("items")
                .get(0)
                .path("skippedInRow")
                .asInt(),
        )

        val first = world.api.get("/api/v1/sources/$source/schedule/fires?limit=1", admin).json
        assertEquals(listOf("skipped_active"), first.pluck("items", "outcome"))
        val cursor = first.path("nextCursor").asString()
        val second = world.api.get("/api/v1/sources/$source/schedule/fires?limit=1&cursor=$cursor", admin).json
        assertEquals(listOf("run_created"), second.pluck("items", "outcome"))
        assertTrue(second.path("nextCursor").isNull)
    }
}
