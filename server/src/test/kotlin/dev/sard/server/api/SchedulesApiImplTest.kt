// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.extension.TenantResolver
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.Sources
import dev.sard.server.scheduler.QUIET_LOOP
import dev.sard.server.scheduler.SPACING
import dev.sard.server.scheduler.ScheduleTables
import dev.sard.server.scheduler.Scheduler
import dev.sard.server.scheduler.Schedules
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertNull

private fun at(text: String): Instant = Instant.parse(text)

/** F3a/F3b: the schedule endpoints over the real schedules, without the HTTP layer; its mapping is mutated. */
@MutFlowTest(includeTargets = [SchedulesApiImpl::class])
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0", QUIET_LOOP, SPACING],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class SchedulesApiImplTest(
    @Autowired private val sources: Sources,
    @Autowired private val schedules: Schedules,
    @Autowired private val scheduler: Scheduler,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc)
    private val tables = ScheduleTables(jdbc, tenant)
    private val api = SchedulesApiImpl(schedules, TenantResolver { tenant.id })

    @BeforeTest
    fun `a tenant with an agent`() {
        clock.now = RUNS_NOW
        tenant.create()
    }

    @AfterTest
    fun `drop the tenant`() = tables.drop()

    private fun source(): UUID = sources.create(tenant.id, tenant.draft("prod-db")).id

    private fun set(
        source: UUID,
        cron: String = "0 * * * *",
        enabled: Boolean = true,
        notifyOnSuccess: Boolean = false,
    ) = MutFlow.underTest { api.setSchedule(source, ScheduleInput(cron, "UTC", enabled, notifyOnSuccess)) }

    private fun get(source: UUID) = MutFlow.underTest { api.getSchedule(source) }

    private fun fires(
        source: UUID,
        cursor: String? = null,
        limit: Int = 50,
    ) = MutFlow.underTest { api.listScheduleFires(source, cursor, limit) }

    private fun tickAt(time: Instant) {
        clock.now = time
        scheduler.tick()
    }

    @Test
    fun `a schedule is set, read back with its flags, and a source without one is not found`() {
        val source = source()
        assertFailsWith<ResourceNotFound> { get(source) }

        val set = set(source, cron = "30 2 * * *", enabled = false, notifyOnSuccess = true)

        assertEquals(source, set.sourceId)
        assertEquals("30 2 * * *" to "UTC", set.cron to set.timezone)
        assertEquals(false to true, set.enabled to set.notifyOnSuccess)
        assertNull(set.nextRunAt)
        assertNull(set.lastRun)
        assertEquals(set, get(source))
        assertEquals(false, set(source).notifyOnSuccess, "a schedule set without the flag has it off")
    }

    @Test
    fun `a schedule names its times, its skips and the latest run it created`() {
        val source = source()
        val created = set(source)
        assertEquals(at("2026-09-30T11:00:00Z"), created.nextRunAt)
        assertEquals(0, created.skippedInRow)
        assertNull(created.lastFiredAt)

        tickAt(at("2026-09-30T11:00:00Z"))
        tickAt(at("2026-09-30T12:00:00Z"))

        val read = get(source)
        assertEquals(at("2026-09-30T13:00:00Z"), read.nextRunAt)
        assertEquals(at("2026-09-30T12:00:00Z"), read.lastFiredAt)
        assertEquals(1, read.skippedInRow)
        val last = assertNotNull(read.lastRun)
        val run = tables.runs().first()
        assertEquals(listOf(run.id, RunTrigger.SCHEDULE, RunStatus.QUEUED), listOf(last.id, last.trigger, last.status))
        assertEquals(listOf(at("2026-09-30T11:00:00Z"), null), listOf(last.queuedAt, last.finishedAt))
        assertEquals(created.createdAt, read.createdAt)
    }

    @Test
    fun `the journal is mapped fire by fire, newest first, and paged by cursor`() {
        val source = source()
        set(source, cron = "* * * * *")
        tickAt(RUNS_NOW.plus(Duration.ofDays(8)))
        tickAt(RUNS_NOW.plus(Duration.ofDays(8)).plusSeconds(2))
        tickAt(RUNS_NOW.plus(Duration.ofDays(8)).plusSeconds(62))

        val all = fires(source)
        assertNull(all.nextCursor)
        assertEquals(3, all.items.size)
        val downtime = all.items.single { it.outcome == ScheduleFireOutcome.SKIPPED_DOWNTIME }
        assertEquals(ScheduleFireKind.SCHEDULE, downtime.kind)
        assertEquals(listOf(10_000, true), listOf(downtime.missedCount, downtime.missedCountCapped))
        assertNotNull(downtime.missedUntil)
        val catchUp = all.items.single { it.kind == ScheduleFireKind.CATCH_UP }
        assertEquals(ScheduleFireOutcome.RUN_CREATED, catchUp.outcome)
        assertEquals(tables.runs().single().id, catchUp.runId)
        assertNull(catchUp.reason)
        assertNull(catchUp.missedCount)
        assertEquals(false, catchUp.missedCountCapped)
        val skipped = all.items.single { it.outcome == ScheduleFireOutcome.SKIPPED_ACTIVE }
        assertEquals(listOf(1, false), listOf(skipped.skippedInRow, skipped.alert))
        assertEquals(all.items.map { it.recordedAt }.sortedDescending(), all.items.map { it.recordedAt })

        val first = fires(source, limit = 2)
        assertEquals(all.items.take(2), first.items)
        val second = fires(source, cursor = assertNotNull(first.nextCursor), limit = 2)
        assertEquals(all.items.drop(2), second.items)
        assertNull(second.nextCursor)
    }

    @Test
    fun `a fire that found the agent revoked names the reason`() {
        val source = source()
        set(source)
        jdbc.update("update agents set revoked_at = ? where id = ?", Timestamp.from(RUNS_NOW), tenant.agentId)

        tickAt(at("2026-09-30T11:00:00Z"))

        val fire = fires(source).items.single()
        assertEquals(ScheduleFireOutcome.SKIPPED_GONE to ScheduleFireReason.AGENT_REVOKED, fire.outcome to fire.reason)
        assertNull(fire.runId)
    }

    @Test
    fun `an unknown source has no schedule and no journal`() {
        assertFailsWith<RuntimeException> { fires(UUID.randomUUID()) }
        assertFailsWith<RuntimeException> { set(UUID.randomUUID()) }
    }
}
