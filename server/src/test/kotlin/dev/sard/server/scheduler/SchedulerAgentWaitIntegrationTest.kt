// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import dev.sard.server.TestAdmin
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RecordingStepsQueued
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.SourceView
import dev.sard.server.runs.Sources
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.Tag
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals

private fun at(text: String): Instant = Instant.parse(text)

/** J1c: a scheduled run waits for an offline agent until the next fire, at most a day; the expiry frees the source. */
@Tag("mutflow-isolated")
@MutFlowTest(
    includeTargets = [Scheduler::class, Firing::class, Schedules::class],
)
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0", QUIET_LOOP, SPACING],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class SchedulerAgentWaitIntegrationTest(
    @Autowired private val sources: Sources,
    @Autowired private val schedules: Schedules,
    @Autowired private val scheduler: Scheduler,
    @Autowired private val queued: RecordingStepsQueued,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc)
    private val tables = ScheduleTables(jdbc, tenant)

    @BeforeTest
    fun `a tenant with an agent`() {
        clock.now = RUNS_NOW
        queued.calls.clear()
        tenant.create()
    }

    @AfterTest
    fun `drop the tenant`() {
        TestAdmin.of(jdbc).execute("drop trigger if exists fail_fires on schedule_fires")
        tables.drop()
    }

    private fun source(name: String = "prod-db"): SourceView = sources.create(tenant.id, tenant.draft(name = name))

    private fun schedule(
        source: SourceView,
        cron: String = "0 * * * *",
    ): ScheduleView {
        val draft = ScheduleDraft(cron, "UTC", enabled = true)
        return MutFlow.underTest { schedules.set(tenant.id, source.id, draft) }
    }

    private fun tickAt(time: Instant) {
        clock.now = time
        MutFlow.underTest { scheduler.tick() }
    }

    private fun waitUntil(runId: UUID): Instant =
        jdbc
            .queryForObject("select agent_wait_until from runs where id = ?", Timestamp::class.java, runId)!!
            .toInstant()

    // J1c: Запуск по расписанию ждёт агента до следующего срабатывания
    @Test
    fun `a scheduled run waits for an offline agent until the next fire of its schedule`() {
        schedule(source(), cron = "0 */6 * * *")

        tickAt(at("2026-09-30T12:00:00Z"))

        assertEquals(at("2026-09-30T18:00:00Z"), waitUntil(tables.runs().single().id))
    }

    // J1c: Ожидание не дольше потолка
    @Test
    fun `a scheduled run of a weekly schedule waits no longer than a day`() {
        schedule(source(), cron = "0 0 * * 0")

        tickAt(at("2026-10-04T00:00:00Z"))

        assertEquals(at("2026-10-05T00:00:00Z"), waitUntil(tables.runs().single().id))
    }

    // J1c: Срабатывание в момент истечения ожидания создаёт новый запуск, а не пропуск
    @Test
    fun `a fire at the moment a wait expires creates a new run, not a skip`() {
        val schedule = schedule(source())
        tickAt(at("2026-09-30T11:00:00Z"))
        val waiting = tables.runs().single().id

        tickAt(at("2026-09-30T12:00:00Z"))

        val expired = jdbc.queryForObject("select status from runs where id = ?", String::class.java, waiting)
        assertEquals("agent_unavailable", expired)
        val fires = tables.fires(schedule.id)
        assertEquals(listOf("run_created", "run_created"), fires.map { it.outcome })
        assertEquals(2, tables.runs().size)
    }
}
