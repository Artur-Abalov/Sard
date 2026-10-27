// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import dev.sard.server.TestAdmin
import dev.sard.server.api.ApiSession
import dev.sard.server.api.RestApiTest
import dev.sard.server.api.RestWorld
import dev.sard.server.api.RestoreWorld
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.persistence.StepLogPartitions
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val JANUARY: Instant = Instant.parse("2027-01-15T00:00:00Z")

/** What the console and REST show after the cleaning: logs that are gone read empty, runs that are gone are 404. */
@RestApiTest
class RetentionRestApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val clock: MovableClock,
    @Autowired sessions: TenantSessions,
    @Autowired logs: StepLogRetention,
    @Autowired settings: RetentionSettings,
    @Autowired private val logPartitions: StepLogPartitions,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val agent = world.agent(tenant)
    private val scene = SceneTenant(jdbc, tenant to agent.agentId)

    /** The cleaning at the moment of the specification; the clock of the server stays, sessions are kept by it. */
    private val retention = RetentionPass(sessions, logs, Clock.fixed(T0, ZoneOffset.UTC), settings) { _, _ -> }

    @AfterTest
    fun `drop the tenant and restore the partitions`() {
        world.close()
        TestAdmin.of(jdbc).execute("drop table if exists step_logs_2027_01")
        TestAdmin.of(jdbc).execute("drop table if exists step_logs_2027_03")
        restorePartitions(logPartitions)
    }

    private fun cleaned(): ApiSession {
        retention.run()
        return world.admin(tenant)
    }

    private fun logs(
        session: ApiSession,
        run: UUID,
        step: UUID,
    ) = world.api.get("/api/v1/runs/$run/steps/$step/logs?afterSeq=0", session)

    /** A run with a snapshot, so that it stays; its first step has [lines] log lines in the partition of January. */
    private fun keptRun(
        lines: Int = 0,
        truncated: Boolean = false,
    ): SceneRun {
        val source = scene.source()
        val run = scene.run(source, old(RUNS_TERM), status = "succeeded")
        scene.snapshot(run.steps.single(), source)
        if (lines > 0) {
            jdbc.createPartition("2027_01")
            scene.logLines(run.steps.single(), lines, JANUARY)
        }
        jdbc.update(
            "update run_steps set log_lines = ?, log_truncated = ? where id = ?",
            lines.toLong(),
            truncated,
            run.steps.single(),
        )
        return run
    }

    @Test
    fun `Строка лога, пришедшая после завершения шага, читается и лежит в секции месяца получения`() {
        jdbc.createPartition("2027_03")
        val run = scene.run(scene.source(), Instant.parse("2027-02-27T23:59:00Z"), status = "succeeded")
        scene.logLines(run.steps.single(), 1, Instant.parse("2027-03-01T00:01:00Z"))
        jdbc.update("update run_steps set log_lines = 1 where id = ?", run.steps.single())

        val answer = logs(world.admin(tenant), run.id, run.steps.single())

        assertEquals(1, answer.json.path("items").size())
        val partition =
            jdbc.queryForObject(
                "select tableoid::regclass::text from step_logs where step_id = ?",
                String::class.java,
                run.steps.single(),
            )
        assertEquals("step_logs_2027_03", partition)
    }

    @Test
    fun `Лог шага, удалённый по сроку, читается пустым списком без ошибки`() {
        val run = keptRun(lines = 1200)
        val before = jdbc.queryForObject("select count(*) from step_logs", Int::class.java)
        val session = cleaned()

        val answer = logs(session, run.id, run.steps.single())

        assertEquals(1200, before)
        assertEquals(200, answer.status)
        assertEquals(0, answer.json.path("items").size())
        assertFalse(answer.json.path("hasMore").asBoolean())
        assertEquals(0, answer.json.path("nextAfterSeq").asLong())
    }

    @Test
    fun `Ответ лога, удалённого по сроку, не отличается от ответа шага без строк`() {
        val deleted = keptRun(lines = 10)
        val empty = keptRun()
        val session = cleaned()

        val a = logs(session, deleted.id, deleted.steps.single())
        val b = logs(session, empty.id, empty.steps.single())

        assertEquals(200, a.status)
        assertEquals(200, b.status)
        assertEquals(a.json.propertyNames().toSet(), b.json.propertyNames().toSet())
        assertEquals(a.json, b.json)
    }

    @Test
    fun `Признак усечения лога сохраняется после удаления секции`() {
        val run = keptRun(lines = 10, truncated = true)
        val session = cleaned()

        val answer = logs(session, run.id, run.steps.single())

        assertEquals(200, answer.status)
        assertEquals(0, answer.json.path("items").size())
        assertTrue(answer.json.path("truncated").asBoolean())
    }

    @Test
    fun `Карточка запуска с удалёнными логами читается полностью`() {
        val run = keptRun(lines = 10)
        val session = cleaned()

        val card = world.api.get("/api/v1/runs/${run.id}", session)

        assertEquals(200, card.status)
        assertEquals("succeeded", card.json.path("status").asString())
        assertEquals(1, card.json.path("steps").size())
        assertEquals(
            "succeeded",
            card.json
                .path("steps")
                .get(0)
                .path("status")
                .asString(),
        )
    }

    @Test
    fun `Удалённый запуск не найден ни он, ни лог его шага, и в списке его нет`() {
        val source = scene.source()
        scene.run(source, T0, status = "succeeded")
        val gone = scene.run(source, old(RUNS_TERM))
        val session = cleaned()

        val card = world.api.get("/api/v1/runs/${gone.id}", session)
        val log = logs(session, gone.id, gone.steps.single())
        val list = world.api.get("/api/v1/runs?sourceId=$source", session)

        assertEquals(404, card.status)
        assertEquals("not_found", card.code)
        assertEquals(404, log.status)
        assertEquals("not_found", log.code)
        assertEquals(200, list.status)
        assertFalse(
            gone.id.toString() in
                (0 until list.json.path("items").size()).map {
                    list.json
                        .path("items")
                        .get(it)
                        .path("id")
                        .asString()
                },
        )
    }

    @Test
    fun `Восстановление из консоли видит все снимки после уборки`() {
        val admin = world.admin(tenant)
        val restores = RestoreWorld(world, tenant, admin)
        val source = restores.source()
        val snapshots = (0 until 5).map { restores.snapshot(source, snapshotId = "%08x".format(it) + "0".repeat(56)) }
        val sourceId = UUID.fromString(source)
        scene.runs(sourceId, 60, old(RUNS_TERM))
        val listed =
            world.api
                .get("/api/v1/sources/$source/snapshots", admin)
                .json
                .path("items")
        val idsBefore = (0 until listed.size()).map { listed.get(it).path("snapshotId").asString() }
        jdbc.update(
            "update runs set finished_at = ? , queued_at = ?, started_at = ? where tenant_id = ?",
            stamp(old(RUNS_TERM)),
            stamp(old(RUNS_TERM)),
            stamp(old(RUNS_TERM)),
            tenant,
        )
        jdbc.update(
            "update run_steps set finished_at = ?, queued_at = ?, dispatched_at = ?, started_at = ? " +
                "where tenant_id = ?",
            stamp(old(RUNS_TERM)),
            stamp(old(RUNS_TERM)),
            stamp(old(RUNS_TERM)),
            stamp(old(RUNS_TERM)),
            tenant,
        )

        val session = cleaned()

        val after =
            world.api
                .get("/api/v1/sources/$source/snapshots", session)
                .json
                .path("items")
        assertEquals(idsBefore, (0 until after.size()).map { after.get(it).path("snapshotId").asString() })
        assertEquals(5, after.size())
        for (id in idsBefore) {
            assertEquals(200, world.api.get("/api/v1/sources/$source/snapshots/$id/restore-options", session).status)
        }
        assertEquals(snapshots.size, scene.count("runs", "status = 'succeeded'"))
    }
}
