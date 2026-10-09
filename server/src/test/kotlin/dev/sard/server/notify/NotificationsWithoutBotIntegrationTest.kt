// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

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
