// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.proto.agent.v1.StepStatus
import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.PluginAction
import dev.sard.server.registration.Registration
import dev.sard.server.registration.RepositoryEntry
import dev.sard.server.registration.VerifierReport
import dev.sard.server.role.AgentRole
import dev.sard.server.verify.VerificationLoop
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val REPOSITORY_ID = "e".repeat(64)

/** J1f REST: "Задание проверки восстановления отменяется администратором" over HTTP and a verifier's stream. */
@RestApiTest
class VerificationCancelApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @Autowired private val hellos: ReconciledHellos,
    @Autowired private val loop: VerificationLoop,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val agent =
        world.agent(tenant, snapshotOf(repositories = listOf(RepositoryEntry("qa", "local", REPOSITORY_ID, ""))))
    private val verifier =
        world.agent(
            tenant,
            snapshotOf(
                plugins = listOf(filesPlugin(actions = listOf(PluginAction.VERIFY))),
                repositories = listOf(RepositoryEntry("shared", "s3", REPOSITORY_ID, "")),
                machineId = OTHER_MACHINE_ID,
            ).copy(verifier = VerifierReport(null, null)),
            hostname = "vrf1",
            role = AgentRole.VERIFIER,
        )
    private val seed = VerificationSeed(world, tenant, admin, verifier)
    private val source =
        world.api
            .post("/api/v1/sources", admin, sourceJson("db-main", agent.agentId))
            .json
            .path("id")
            .asString()

    @AfterTest
    fun `drop the tenants`() = world.close()

    /** A whole snapshot of the source, as a succeeded backup leaves it. */
    private fun snapshot() {
        val run =
            UUID.fromString(
                world.api
                    .post("/api/v1/sources/$source/runs", admin, "")
                    .json
                    .path("id")
                    .asString(),
            )
        world.forceRun(run, "succeeded")
        val step = jdbc.queryForObject("select id from run_steps where run_id = ?", UUID::class.java, run)
        jdbc.update(
            "insert into snapshots (id, tenant_id, source_id, step_id, agent_id, repository_name, repository_id, " +
                "snapshot_id, total_bytes, added_bytes, created_at, partial) " +
                "values (?, ?, ?, ?, ?, 'qa', ?, ?, 10, 1, ?, false)",
            UUID.randomUUID(),
            tenant,
            UUID.fromString(source),
            step,
            agent.agentId,
            REPOSITORY_ID,
            "snap-${UUID.randomUUID()}",
            Timestamp.from(world.clock.now),
        )
    }

    /** The verifier, connected and greeted. */
    private fun connected(): FakeAgent {
        val fake = FakeAgent(world.connect(verifier).also { it.hello() })
        hellos.await(verifier.agentId)
        return fake
    }

    private fun verifyNow(): String {
        snapshot()
        return world.api
            .post("/api/v1/sources/$source/verifications", admin, "{}")
            .json
            .path("id")
            .asString()
    }

    private fun cancel(
        job: String,
        session: ApiSession? = admin,
    ) = world.api.post("/api/v1/verification-jobs/$job/cancel", session, null)

    private fun openJob(): JsonNode =
        world.api
            .get("/api/v1/sources/$source/verification", admin)
            .json
            .path("openJob")

    private fun history(): List<JsonNode> =
        world.api
            .get("/api/v1/sources/$source/verifications", admin)
            .json
            .path("items")
            .list()

    /** A job handed to the connected verifier, which accepted its step: the server sees the attempt running. */
    private fun running(fake: FakeAgent): Pair<String, String> {
        val job = verifyNow()
        loop.tick()
        val step = fake.nextStep()
        fake.progress(step)
        eventually { assertEquals("running", openJob().path("status").asString()) }
        return job to step
    }

    @Test
    fun `Отмена задания в очереди без попытки закрывает его, ответ 200, openJob null`() {
        val job = verifyNow()

        val response = cancel(job)

        assertEquals(200, response.status, response.toString())
        assertTrue(response.json.path("openJob").isNull)
        val record = history().single()
        assertEquals(listOf("cancelled", "admin"), listOf("status", "cancelledBy").map { record.path(it).asString() })
        assertEquals(world.clock.now.toString(), record.path("cancelledAt").asString())
        assertEquals(202, world.api.post("/api/v1/sources/$source/verifications", admin, "{}").status)
    }

    @Test
    fun `Отмена идущей попытки посылает верификатору CancelStep, openJob running с отменой pending`() {
        val fake = connected()
        val (job, step) = running(fake)

        val response = cancel(job)

        assertEquals(200, response.status, response.toString())
        assertEquals(step, fake.nextCancel())
        val open = response.json.path("openJob")
        assertEquals(listOf("running", job), listOf("status", "id").map { open.path(it).asString() })
        val cancellation = open.path("cancellation")
        assertEquals(
            listOf("admin", world.clock.now.toString(), "pending"),
            listOf("requestedBy", "requestedAt", "result").map { cancellation.path(it).asString() },
        )
    }

    @Test
    fun `Подтверждение верификатора закрывает задание записью cancelled без новой попытки`() {
        val fake = connected()
        val (job, step) = running(fake)
        cancel(job)
        fake.result(step, StepStatus.STEP_STATUS_CANCELLED, "cancelled by the server")

        eventually {
            loop.tick()
            assertEquals(listOf("cancelled"), history().map { it.path("status").asString() })
        }

        assertEquals(listOf("cancelled"), history().map { it.path("status").asString() })
        assertEquals(
            1,
            jdbc.queryForObject(
                "select count(*) from verification_jobs where id = ? and status = 'closed'",
                Int::class.java,
                UUID.fromString(job),
            ),
        )
        // The cycle's own job of the moment may follow; it is another job, not a second attempt of this one.
        assertTrue(openJob().isNull || openJob().path("id").asString() != job)
    }

    @Test
    fun `Отмена запуска-попытки через REST запусков отменяет её задание`() {
        val fake = connected()
        val (job, step) = running(fake)
        val sql = "select run_id from verification_jobs where id = ?"
        val run = jdbc.queryForObject(sql, UUID::class.java, UUID.fromString(job))

        val response = world.api.post("/api/v1/runs/$run/cancel", admin, null)

        assertEquals(200, response.status, response.toString())
        assertEquals("cancelling", response.json.path("outcome").asString())
        assertEquals("pending", openJob().path("cancellation").path("result").asString())
        assertEquals(step, fake.nextCancel())
    }

    @Test
    fun `Повторная отмена отвечает 200 и не меняет запись отмены`() {
        val fake = connected()
        val (job, _) = running(fake)
        val first = cancel(job).json.path("openJob").path("cancellation")
        world.clock.now = world.clock.now.plusSeconds(5)

        val second = cancel(job)

        assertEquals(200, second.status)
        assertEquals(first, second.json.path("openJob").path("cancellation"))
    }

    @Test
    fun `Отмена закрытого задания отвечает 409 run_finished и запись не меняется`() {
        val job = verifyNow()
        cancel(job)
        val before = history()

        val again = cancel(job)

        assertEquals(409, again.status)
        assertEquals("run_finished", again.code)
        assertEquals(before, history())
    }

    @Test
    fun `Отмена задания другого тенанта отвечает 404, задание не меняется`() {
        val job = verifyNow()
        val other = world.admin(world.tenant())

        val response = cancel(job, session = other)

        assertEquals(404, response.status)
        assertEquals("not_found", response.code)
        assertEquals(job, openJob().path("id").asString())
        assertTrue(openJob().path("cancellation").isNull)
        assertEquals(404, cancel(UUID.randomUUID().toString()).status)
    }

    @Test
    fun `Отмена задания без сессии отвечает 401, с чужим Origin 403, задание не меняется`() {
        val job = verifyNow()

        val anonymous = cancel(job, session = null)
        val strange = mapOf("Origin" to "https://evil.example")
        val foreign =
            world.api.send("POST", "/api/v1/verification-jobs/$job/cancel", admin, null, strange)

        assertEquals(401, anonymous.status)
        assertEquals("unauthenticated", anonymous.code)
        assertEquals(403, foreign.status)
        assertEquals("origin_rejected", foreign.code)
        assertEquals(job, openJob().path("id").asString())
        assertEquals(emptyList(), history())
    }

    @Test
    fun `Вердикт, пришедший раньше отмены, остаётся итогом, отмена отвечает 409`() {
        val fake = connected()
        val (job, step) = running(fake)
        fake.result(step, StepStatus.STEP_STATUS_SUCCEEDED, "no verdict, as an old agent")
        eventually {
            val sql = "select status from run_steps where id = ?"
            val status = jdbc.queryForObject(sql, String::class.java, UUID.fromString(step))
            assertEquals("succeeded", status)
        }

        val response = cancel(job)

        assertEquals(409, response.status)
        assertEquals("run_finished", response.code)
        assertTrue(openJob().isNull)
        assertEquals(listOf("cannot_verify"), history().map { it.path("status").asString() })
    }

    // --- the record of a cancelled job in the overview and the report

    private fun record(
        source: UUID,
        status: String,
        ago: Duration,
    ) = seed.record(source, agent, VerificationSeed.Record(status, world.clock.now - ago))

    private fun figures(): List<Pair<String, Int>> {
        val json =
            world.api
                .get("/api/v1/overview", admin)
                .json
                .path("verification")
        val names = listOf("enabled", "verifiedLastDay", "failed", "notHeld", "stale", "neverVerified", "disabled")
        return names.map { it to json.path(it).asInt() }
    }

    @Test
    fun `Отменённая проверка не входит в счётчики обзора, статус источника смотрит сквозь неё`() {
        val passed = seed.source("a", agent)
        record(passed, "passed", Duration.ofHours(3))
        record(passed, "cancelled", Duration.ofHours(1))
        val failed = seed.source("b", agent)
        record(failed, "failed", Duration.ofHours(3))
        record(failed, "cancelled", Duration.ofHours(1))

        val expected =
            listOf(
                "enabled" to 3,
                "verifiedLastDay" to 1,
                "failed" to 1,
                "notHeld" to 0,
                "stale" to 0,
                "neverVerified" to 1,
                "disabled" to 0,
            )
        assertEquals(expected, figures())
        val state = world.api.get("/api/v1/sources/$passed/verification", admin).json
        assertEquals("passed", state.path("status").asString())
    }

    @Test
    fun `Отчёт показывает отменённую проверку строкой и своим числом, а не как не состоявшуюся`() {
        val cron = """{"cron":"30 4 * * *","timezone":"UTC"}"""
        world.api.send("PUT", "/api/v1/verification-schedule", admin, cron)
        val named = seed.source("report", agent)
        val day = { text: String -> Instant.parse("${text}T12:00:00Z") }
        val snapshot = seed.snapshot(named, agent, day("2026-10-01"))
        for ((status, at) in listOf("passed" to "2026-10-10", "cancelled" to "2026-10-11", "failed" to "2026-10-12")) {
            seed.record(named, agent, VerificationSeed.Record(status, day(at), snapshot = snapshot))
        }
        val sql = "select id from restore_verifications where status = 'cancelled'"
        val cancelled = jdbc.queryForObject(sql, UUID::class.java)

        val json = world.api.get("/api/v1/sources/$named/verification-report?from=2026-10-01&to=2026-10-31", admin).json

        val summary = json.path("summary")
        assertEquals(
            listOf(3, 1, 1, 1, 0),
            listOf("total", "passed", "failed", "cancelled", "notHeld").map { summary.path(it).asInt() },
        )
        val item = json.path("items").list().single { it.path("id").asString() == cancelled.toString() }
        assertEquals(listOf("cancelled", "admin"), listOf("status", "cancelledBy").map { item.path(it).asString() })
        assertEquals(
            1,
            json
                .path("failures")
                .list()
                .single()
                .path("records")
                .asInt(),
        )
        val one = world.api.get("/api/v1/verifications/$cancelled", admin).json
        assertEquals(listOf("cancelled", "admin"), listOf("status", "cancelledBy").map { one.path(it).asString() })
        assertEquals(day("2026-10-11").toString(), one.path("cancelledAt").asString())
    }
}
