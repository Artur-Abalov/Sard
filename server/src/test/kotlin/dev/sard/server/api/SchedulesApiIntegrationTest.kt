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

    private fun preview(query: String) = world.api.get("/api/v1/schedule-preview?$query", admin)

    private val longCron = "0,".repeat(100) + "0 2 * * *"

    @Test
    fun `a preview names the cron as it is saved, the words, three fires and the zone`() {
        val json = preview("cron=%20%200%20%202%20*%20*%20*&timezone=Europe/Berlin&lang=ru").json

        assertEquals("0 2 * * *", json.path("cron").asString())
        assertEquals("Europe/Berlin", json.path("timezone").asString())
        assertEquals("Каждый день в 02:00", json.path("description").asString())
        assertEquals(3, json.path("nextFires").size())
        assertTrue(json.path("nextFires").list().all { it.asString().endsWith("Z") })
        assertEquals(false, json.path("tooFrequent").asBoolean())
    }

    @Test
    fun `a preview without a language is in English and without a zone is in the server's`() {
        val json = preview("cron=*/5%20*%20*%20*%20*").json

        assertEquals("Every 5 minutes", json.path("description").asString())
        assertEquals(true, json.path("tooFrequent").asBoolean())
        assertTrue(json.path("timezone").asString().isNotEmpty())
    }

    @Test
    fun `a preview writes nothing and needs a session`() {
        val before = world.count("schedules", tenant)
        preview("cron=0%202%20*%20*%20*&timezone=UTC")
        assertEquals(before, world.count("schedules", tenant))
        assertEquals(0, world.count("schedule_fires", tenant))
        assertEquals(401, world.api.get("/api/v1/schedule-preview?cron=0%202%20*%20*%20*", null).status)
    }

    @Test
    fun `a preview refuses what saving refuses, naming the field`() {
        val wrong =
            listOf(
                "cron=&timezone=UTC" to "cron",
                "cron=0%202%20*%20*&timezone=UTC" to "cron",
                "cron=0%202%20*%20*%20*&timezone=%2B03:00" to "timezone",
                "cron=0%202%20*%20*%20*&timezone=" to "timezone",
                "cron=0%202%20*%20*%20*&timezone=UTC&lang=de" to "lang",
            )
        for ((query, field) in wrong) {
            val response = preview(query)
            assertEquals(422, response.status, query)
            assertEquals("validation_failed", response.code)
            assertEquals(listOf(field), response.errorFields(), query)
        }
    }

    @Test
    fun `a cron longer than 200 characters is a 422 on the cron field, and the old schedule stays`() {
        val source = source()
        val logs =
            captureLogs {
                val created = put(source, cron = longCron)
                assertEquals(422, created.status, created.toString())
                assertEquals(listOf("cron"), created.errorFields())
                assertEquals(
                    "cron is longer than 200 characters",
                    created.json
                        .path("errors")
                        .get(0)
                        .path("message")
                        .asString(),
                )
                assertTrue("0,0,0,0,0" !in created.body)
                assertEquals(404, world.api.get("/api/v1/sources/$source/schedule", admin).status)

                put(source, cron = "0 2 * * *")
                val replaced = put(source, cron = longCron)
                assertEquals(422, replaced.status, replaced.toString())
                assertEquals(
                    "0 2 * * *",
                    world.api
                        .get("/api/v1/sources/$source/schedule", admin)
                        .json
                        .path("cron")
                        .asString(),
                )

                val previewed = preview("cron=${longCron.replace(" ", "%20")}&timezone=UTC")
                assertEquals(422, previewed.status, previewed.toString())
                assertEquals(listOf("cron"), previewed.errorFields())
            }
        assertTrue(logs.none { "database unavailable" in it })
    }

    @Test
    fun `notifyOnSuccess is off by default, is kept, and changing it alone leaves the next fire`() {
        val source = source()
        val created = put(source).json
        assertEquals(false, created.path("notifyOnSuccess").asBoolean())
        assertTrue(created.path("lastRun").isNull)

        clock.now = T0.plus(1, ChronoUnit.HOURS)
        val body = """{"cron":"0 * * * *","timezone":"UTC","enabled":true,"notifyOnSuccess":true}"""
        val saved = world.api.send("PUT", "/api/v1/sources/$source/schedule", admin, body).json

        assertEquals(true, saved.path("notifyOnSuccess").asBoolean())
        assertEquals(created.path("nextRunAt"), saved.path("nextRunAt"))
        assertEquals(saved, world.api.get("/api/v1/sources/$source/schedule", admin).json)
    }

    @Test
    fun `a schedule names its latest run and a catch-up run its period`() {
        val source = source()
        put(source)
        clock.now = nextHour
        scheduler.tick()
        val run =
            world.api
                .get("/api/v1/runs", admin)
                .json
                .path("items")
                .get(0)

        val lastRun =
            world.api
                .get("/api/v1/sources/$source/schedule", admin)
                .json
                .path("lastRun")
        assertEquals(run.path("id"), lastRun.path("id"))
        assertEquals(listOf("schedule", "queued"), listOf("trigger", "status").map { lastRun.path(it).asString() })
        assertTrue(run.path("catchUp").isNull)

        world.forceRun(java.util.UUID.fromString(run.path("id").asString()), "succeeded")
        clock.now = nextHour.plus(3, ChronoUnit.HOURS).plusSeconds(1800)
        scheduler.tick()
        scheduler.tick()
        val catchUp =
            world.api
                .get("/api/v1/runs", admin)
                .json
                .path("items")
                .get(0)
        assertEquals("catch_up", catchUp.path("trigger").asString())
        val period = catchUp.path("catchUp")
        assertEquals(nextHour.plus(1, ChronoUnit.HOURS).toString(), period.path("missedFrom").asString())
        assertEquals(nextHour.plus(3, ChronoUnit.HOURS).toString(), period.path("missedUntil").asString())
        assertEquals(
            listOf(3, false, "UTC"),
            listOf(
                period.path("missedCount").asInt(),
                period.path("missedCountCapped").asBoolean(),
                period.path("timezone").asString(),
            ),
        )
        assertEquals(
            period,
            world.api
                .get("/api/v1/runs/${catchUp.path("id").asString()}", admin)
                .json
                .path("catchUp"),
        )
        val fires =
            world.api
                .get("/api/v1/sources/$source/schedule/fires", admin)
                .json
                .path("items")
        assertTrue(fires.list().all { !it.path("missedCountCapped").asBoolean() })
    }
}
