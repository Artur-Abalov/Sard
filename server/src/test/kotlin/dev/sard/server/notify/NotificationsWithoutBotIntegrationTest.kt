// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.TestAdmin
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.Sources
import dev.sard.server.runs.StepOutcome
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepTransitions
import dev.sard.server.scheduler.ScheduleDraft
import dev.sard.server.scheduler.Schedules
import io.micrometer.core.instrument.MeterRegistry
import org.junit.jupiter.api.extension.ExtendWith
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * S9a, test strategy 6 (ADR 0024, answer В3): without a bot the server starts, warns once, and
 * plans nothing however many runs finish, so the queue cannot grow.
 */
@ExtendWith(OutputCaptureExtension::class)
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    // Its own context: the warning is logged while this class's context starts.
    properties = ["spring.grpc.server.port=0", "sard.notify.tick-interval=1h", "sard.notify.batch=7"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class, NotifyTestConfiguration::class)
class NotificationsWithoutBotIntegrationTest(
    @Autowired private val service: NotificationService,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val steps: StepTransitions,
    @Autowired private val schedules: Schedules,
    @Autowired private val clock: MovableClock,
    @Autowired private val meters: MeterRegistry,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc).create()

    @AfterTest
    fun `drop the tenant`() {
        val verification =
            listOf(
                "no_verifier_episodes",
                "verification_stale_episodes",
                "restore_verifications",
                "verification_jobs",
                "snapshots",
            )
        val admin = TestAdmin.of(jdbc)
        for (table in verification) admin.update("delete from $table where tenant_id = ?", tenant.id)
        jdbc.update("delete from schedule_fires where tenant_id = ?", tenant.id)
        jdbc.update("delete from schedules where tenant_id = ?", tenant.id)
        tenant.drop()
    }

    @Test
    fun `an alert about skipped fires plans nothing without a bot`() {
        clock.now = RUNS_NOW
        val source = sources.create(tenant.id, tenant.draft("db-main"))
        val schedule = schedules.set(tenant.id, source.id, ScheduleDraft("0 2 * * *", "UTC", enabled = true))
        jdbc.update(
            "insert into schedule_fires (id, tenant_id, schedule_id, kind, scheduled_for, outcome, reason, " +
                "skipped_in_row, alert, recorded_at) " +
                "values (?, ?, ?, 'schedule', ?, 'refused', 'unknown_plugin', 3, true, ?)",
            java.util.UUID.randomUUID(),
            tenant.id,
            schedule.id,
            java.sql.Timestamp.from(RUNS_NOW),
            java.sql.Timestamp.from(RUNS_NOW),
        )

        repeat(3) { service.tick() }

        assertEquals(0, jdbc.queryForObject("select count(*) from notification_deliveries", Int::class.java))
    }

    private val at = java.sql.Timestamp.from(RUNS_NOW)

    /** What a record of a verification refers to. */
    private class Verifiable(
        val source: java.util.UUID,
        val snapshot: java.util.UUID,
        val step: java.util.UUID,
        val job: java.util.UUID,
        val verifier: java.util.UUID,
    )

    /** A source with a snapshot, a closed job and a verifier. */
    private fun verifiable(): Verifiable {
        val source = sources.create(tenant.id, tenant.draft("db-main")).id
        val run = runs.start(tenant.id, source).id
        tenant.finish(run)
        val step = jdbc.queryForObject("select id from run_steps where run_id = ?", java.util.UUID::class.java, run)!!
        val (snapshot, job, verifier) = List(3) { java.util.UUID.randomUUID() }
        jdbc.update(
            "insert into agents (id, tenant_id, hostname, registered_at, role) values (?, ?, 'vrf1', ?, 'verifier')",
            verifier,
            tenant.id,
            at,
        )
        jdbc.update(
            "insert into snapshots (id, tenant_id, source_id, step_id, agent_id, repository_name, repository_id, " +
                "snapshot_id, total_bytes, added_bytes, created_at, partial) " +
                "values (?, ?, ?, ?, ?, 'main', 'r', 's', 10, 1, ?, false)",
            snapshot,
            tenant.id,
            source,
            step,
            tenant.agentId,
            at,
        )
        jdbc.update(
            "insert into verification_jobs (id, tenant_id, source_id, snapshot_ref, repository_id, plugin, reason, " +
                "status, created_at, deadline, closed_at) " +
                "values (?, ?, ?, ?, ?, 'postgresql', 'cycle', 'closed', ?, ?, ?)",
            job,
            tenant.id,
            source,
            snapshot,
            "c".repeat(64),
            at,
            at,
            at,
        )
        return Verifiable(source, snapshot, step, job, verifier)
    }

    @Test
    fun `a failed verification, a stale episode and no verifier plan nothing without a bot`(output: CapturedOutput) {
        clock.now = RUNS_NOW
        val v = verifiable()
        jdbc.update(
            "insert into restore_verifications (id, tenant_id, job_id, source_id, snapshot_id, step_id, " +
                "verifier_agent_id, key_holders, status, level, verified_at) " +
                "values (?, ?, ?, ?, ?, ?, ?, 1, 'failed', 3, ?)",
            java.util.UUID.randomUUID(),
            tenant.id,
            v.job,
            v.source,
            v.snapshot,
            v.step,
            v.verifier,
            at,
        )
        jdbc.update(
            "insert into verification_stale_episodes (id, tenant_id, source_id, counted_from, opened_at, " +
                "threshold_days, timezone) values (?, ?, ?, ?, ?, 2, 'UTC')",
            java.util.UUID.randomUUID(),
            tenant.id,
            v.source,
            at,
            at,
        )
        jdbc.update(
            "insert into no_verifier_episodes (id, tenant_id, sources, opened_at) values (?, ?, 1, ?)",
            java.util.UUID.randomUUID(),
            tenant.id,
            at,
        )

        repeat(3) { service.tick() }

        assertEquals(0, jdbc.queryForObject("select count(*) from notification_deliveries", Int::class.java))
        val problems = output.all.lines().filter { (" WARN " in it || " ERROR " in it) && ".server.notify." in it }
        assertEquals(1, problems.size, "only the startup warning: $problems")
    }

    @Test
    fun `the server starts without a bot, warns, and the queue stays empty`(output: CapturedOutput) {
        clock.now = RUNS_NOW
        repeat(3) {
            val run = runs.start(tenant.id, sources.create(tenant.id, tenant.draft("db-$it")).id)
            val step = run.steps.single().id
            assertTrue(steps.claim(tenant.id, step))
            assertTrue(steps.finished(tenant.id, step, StepOutcome(StepState.FAILED, "disk full")))
        }

        repeat(3) { service.tick() }

        val off = "Notifications are off: no channel (SARD_TELEGRAM_BOT_TOKEN, SARD_TELEGRAM_CHAT_ID)"
        assertEquals(1, output.all.lines().count { off in it }, "the startup warning is logged once")
        val problems = output.all.lines().filter { (" WARN " in it || " ERROR " in it) && ".server.notify." in it }
        assertEquals(1, problems.size, "no other warning or error of the notification code: $problems")
        val total = jdbc.queryForObject("select count(*) from notification_deliveries", Int::class.java)
        assertEquals(0, total)
        assertEquals(0.0, meters.get("sard.notify.pending").gauge().value())
    }
}
