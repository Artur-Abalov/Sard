// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RUNS_RACE_WAIT
import dev.sard.server.runs.RecordingStepsQueued
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.SourceView
import dev.sard.server.runs.Sources
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.security.SecureRandom
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val HOURLY = "0 * * * *"
private const val EVERY_MINUTE = "* * * * *"

private fun at(text: String): Instant = Instant.parse(text)

/**
 * F3a verification 1-7: a fire is neither lost nor doubled. RUNS_NOW is 2026-09-30T10:00:00Z. Mutants run
 * under single-threaded ticks only; the races call [Scheduler.tick] directly.
 */
@MutFlowTest
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0", QUIET_LOOP, SPACING],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class SchedulerIntegrationTest(
    @Autowired private val sources: Sources,
    @Autowired private val schedules: Schedules,
    @Autowired private val scheduler: Scheduler,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val runs: Runs,
    @Autowired private val queued: RecordingStepsQueued,
    @Autowired private val settings: SchedulerSettings,
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
        jdbc.execute("drop trigger if exists fail_fires on schedule_fires")
        tables.drop()
    }

    private fun source(name: String = "prod-db"): SourceView = sources.create(tenant.id, tenant.draft(name = name))

    private fun schedule(
        source: SourceView,
        cron: String = HOURLY,
        zone: String = "UTC",
    ): ScheduleView = set(source, ScheduleDraft(cron, zone, enabled = true))

    private fun set(
        source: SourceView,
        draft: ScheduleDraft,
    ): ScheduleView = MutFlow.underTest { schedules.set(tenant.id, source.id, draft) }

    private fun tickAt(time: Instant) {
        clock.now = time
        MutFlow.underTest { scheduler.tick() }
    }

    /** A second server on the same database: its own scheduler, nothing shared but the tables. */
    private fun anotherServer() = Scheduler(sessions, clock, runs, UuidV7(clock, SecureRandom()), queued, settings)

    private fun nextRunAt(scheduleId: UUID): Instant? =
        jdbc
            .queryForObject("select next_run_at from schedules where id = ?", Timestamp::class.java, scheduleId)
            ?.toInstant()

    @Test
    fun `1 - a schedule fires once at its moment and moves to the next one`() {
        val source = source()
        val schedule = schedule(source)
        assertEquals(at("2026-09-30T11:00:00Z"), schedule.nextRunAt)

        tickAt(at("2026-09-30T10:59:59Z"))
        assertEquals(emptyList(), tables.runs())

        tickAt(at("2026-09-30T11:00:00Z"))
        tickAt(at("2026-09-30T11:00:05Z"))

        val run = tables.runs().single()
        assertEquals(RunRow(run.id, source.id, "schedule", schedule.id), run)
        val fire = FireRow("schedule", at("2026-09-30T11:00:00Z"), "run_created", run.id, null, null, 0, false)
        assertEquals(listOf(fire), tables.fires(schedule.id))
        assertEquals(at("2026-09-30T12:00:00Z"), nextRunAt(schedule.id))
        assertEquals(listOf(tenant.id to tenant.agentId), queued.calls)
    }

    @Test
    fun `2 - an active run skips the fire visibly, counts skips in a row and alerts once at the threshold`() {
        val schedule = schedule(source())
        tickAt(at("2026-09-30T11:00:00Z"))
        val active = tables.runs().single().id

        for (hour in 12..15) tickAt(at("2026-09-30T$hour:00:00Z"))

        assertEquals(1, tables.runs().size)
        val skips = tables.fires(schedule.id).drop(1)
        assertEquals(listOf("skipped_active"), skips.map { it.outcome }.distinct())
        assertEquals(listOf(active), skips.map { it.runId }.distinct())
        assertEquals(listOf(1, 2, 3, 4), skips.map { it.skippedInRow })
        assertEquals(listOf(false, false, true, false), skips.map { it.alert })

        tables.finishAll(at("2026-09-30T15:30:00Z"))
        tickAt(at("2026-09-30T16:00:00Z"))

        val created = tables.fires(schedule.id).last()
        assertEquals("run_created", created.outcome)
        assertEquals(0, created.skippedInRow)
        assertEquals(2, tables.runs().size)
    }

    @Test
    fun `3 - a server that stops in the middle of a fire neither loses nor doubles it`() {
        val schedule = schedule(source())
        // The transaction fails after the run is written, before the fire is recorded: the server died.
        jdbc.execute(
            """
            create or replace function fail_fire() returns trigger language plpgsql as
            ${'$'}${'$'} begin raise exception 'server stopped'; end ${'$'}${'$'}
            """.trimIndent(),
        )
        jdbc.execute(
            "create trigger fail_fires before insert on schedule_fires for each row execute function fail_fire()",
        )

        tickAt(at("2026-09-30T11:00:00Z"))

        assertEquals(emptyList(), tables.runs())
        assertEquals(emptyList(), tables.fires(schedule.id))
        assertEquals(at("2026-09-30T11:00:00Z"), nextRunAt(schedule.id))

        jdbc.execute("drop trigger fail_fires on schedule_fires")
        val restarted = anotherServer()
        clock.now = at("2026-09-30T11:00:20Z")
        restarted.tick()
        // Restarted once more right after the fire committed: nothing fires twice.
        anotherServer().tick()

        assertEquals(1, tables.runs().size)
        assertEquals(listOf("run_created"), tables.fires(schedule.id).map { it.outcome })
        assertEquals(at("2026-09-30T12:00:00Z"), nextRunAt(schedule.id))
    }

    @Test
    fun `4 - a downtime of several fires is one catch-up, the rest journaled as missed`() {
        val schedule = schedule(source())

        tickAt(at("2026-09-30T14:30:00Z"))

        assertEquals(emptyList(), tables.runs(), "the catch-up waits for its slot")
        val downtime = FireRow("schedule", at("2026-09-30T11:00:00Z"), "skipped_downtime", null, null, 4, 0, false)
        assertEquals(listOf(downtime), tables.fires(schedule.id))
        assertEquals(at("2026-09-30T15:00:00Z"), nextRunAt(schedule.id))

        tickAt(at("2026-09-30T14:30:01Z"))
        tickAt(at("2026-09-30T14:30:02Z"))

        val run = tables.runs().single()
        assertEquals("catch_up", run.trigger)
        val catchUp = tables.fires(schedule.id).last()
        val expected = FireRow("catch_up", at("2026-09-30T14:30:00Z"), "run_created", run.id, null, null, 0, false)
        assertEquals(expected, catchUp)
    }

    @Test
    fun `4 - catch-ups of many sources after a downtime are spread, not started in the same second`() {
        val scheduled = (1..3).map { schedule(source("db-$it")) }

        tickAt(at("2026-09-30T14:30:00Z"))
        val slots =
            scheduled.map {
                jdbc
                    .queryForObject("select catch_up_at from schedules where id = ?", Timestamp::class.java, it.id)!!
                    .toInstant()
            }
        val offsets = slots.map { Duration.between(at("2026-09-30T14:30:00Z"), it).seconds }
        assertEquals(listOf(0L, 10L, 20L), offsets.sorted())

        tickAt(at("2026-09-30T14:30:09Z"))
        assertEquals(1, tables.runs().size)
        tickAt(at("2026-09-30T14:30:19Z"))
        assertEquals(2, tables.runs().size)
        tickAt(at("2026-09-30T14:30:29Z"))
        assertEquals(listOf("catch_up"), tables.runs().map { it.trigger }.distinct())
        assertEquals(3, tables.runs().size)
    }

    /** Ticks every minute from [from] until [to], finishing runs in between as an agent would. */
    private fun everyMinute(
        from: Instant,
        to: Instant,
    ) {
        generateSequence(from) { it.plusSeconds(60) }.takeWhile { it <= to }.forEach {
            tickAt(it)
            tables.finishAll(it.plusSeconds(30))
        }
    }

    @Test
    fun `5 - a fixed hour fires once on the night the clock falls back`() {
        clock.now = at("2026-10-31T12:00:00Z")
        val schedule = schedule(source(), "30 1 * * *", "America/New_York")

        everyMinute(at("2026-11-01T04:00:00Z"), at("2026-11-01T07:30:00Z"))

        assertEquals(listOf(at("2026-11-01T05:30:00Z")), tables.fires(schedule.id).map { it.scheduledFor })
        assertEquals(1, tables.runs().size)
    }

    @Test
    fun `5 - a fixed hour skipped by the clock springing forward fires once, at the transition`() {
        clock.now = at("2027-03-27T12:00:00Z")
        val schedule = schedule(source(), "30 2 * * *", "Europe/Berlin")

        everyMinute(at("2027-03-28T00:00:00Z"), at("2027-03-28T03:00:00Z"))

        assertEquals(listOf(at("2027-03-28T01:00:00Z")), tables.fires(schedule.id).map { it.scheduledFor })
        assertEquals(1, tables.runs().size)
    }

    @Test
    fun `6 - two schedulers on one database fire each schedule once`() {
        val scheduled = (1..12).map { schedule(source("db-$it"), EVERY_MINUTE) }
        val servers = listOf(scheduler, anotherServer())
        val pool = Executors.newFixedThreadPool(servers.size)
        try {
            for (minute in 1..4) {
                clock.now = RUNS_NOW.plusSeconds(60L * minute)
                val start = CountDownLatch(1)
                val ticks = servers.map { server -> pool.submit { start.await().also { server.tick() } } }
                start.countDown()
                ticks.forEach { it.get(RUNS_RACE_WAIT.seconds, TimeUnit.SECONDS) }
                tables.finishAll(clock.now.plusSeconds(30))
            }
        } finally {
            pool.shutdownNow()
        }

        val expected = (1..4).map { RUNS_NOW.plusSeconds(60L * it) }
        for (schedule in scheduled) {
            val fires = tables.fires(schedule.id)
            assertEquals(expected, fires.map { it.scheduledFor }, "fires of ${schedule.id}")
            assertEquals(listOf("run_created"), fires.map { it.outcome }.distinct())
        }
        assertEquals(12 * 4, tables.runs().size)
    }

    @Test
    fun `7 - a deleted source's schedule does not fire and leaves no record`() {
        val source = source()
        val schedule = schedule(source)
        sources.delete(tenant.id, source.id)

        tickAt(at("2026-09-30T11:00:00Z"))

        assertEquals(emptyList(), tables.runs())
        assertEquals(emptyList(), tables.fires(schedule.id))
    }

    @Test
    fun `7 - a revoked agent's schedule records the skip and creates no run`() {
        val schedule = schedule(source())
        jdbc.update("update agents set revoked_at = ? where id = ?", Timestamp.from(RUNS_NOW), tenant.agentId)

        tickAt(at("2026-09-30T11:00:00Z"))

        assertEquals(emptyList(), tables.runs())
        val fire = tables.fires(schedule.id).single()
        assertEquals("skipped_gone" to "agent_revoked", fire.outcome to fire.reason)
        assertEquals(1, fire.skippedInRow)
    }

    @Test
    fun `7 - a disabled schedule does not fire and has no next fire`() {
        val source = source()
        val schedule = schedule(source)
        set(source, ScheduleDraft(HOURLY, "UTC", enabled = false))

        tickAt(at("2026-09-30T11:00:00Z"))

        assertEquals(emptyList(), tables.runs())
        assertEquals(emptyList(), tables.fires(schedule.id))
        assertNull(nextRunAt(schedule.id))
    }

    @Test
    fun `a plugin the agent no longer offers refuses the fire with its reason and counts as a skip`() {
        val schedule = schedule(source())
        jdbc.update("delete from agent_plugins where tenant_id = ?", tenant.id)

        tickAt(at("2026-09-30T11:00:00Z"))

        assertEquals(emptyList(), tables.runs())
        val fire = tables.fires(schedule.id).single()
        assertEquals(Triple("refused", "unknown_plugin", 1), Triple(fire.outcome, fire.reason, fire.skippedInRow))
    }

    @Test
    fun `a manual start racing a scheduled fire leaves one active run and a journal that names it`() {
        repeat(5) { round ->
            val source = source("race-$round")
            val schedule = schedule(source)
            val time = at("2026-09-30T11:00:00Z").plusSeconds(3600L * round)
            clock.now = time
            jdbc.update("update schedules set next_run_at = ? where id = ?", Timestamp.from(time), schedule.id)
            val pool = Executors.newFixedThreadPool(2)
            val start = CountDownLatch(1)
            try {
                val tick = pool.submit { start.await().also { scheduler.tick() } }
                val manual =
                    pool.submit<UUID?> {
                        start.await()
                        runCatching { runs.start(tenant.id, source.id).id }.getOrNull()
                    }
                start.countDown()
                tick.get(RUNS_RACE_WAIT.seconds, TimeUnit.SECONDS)
                val manualRun = manual.get(RUNS_RACE_WAIT.seconds, TimeUnit.SECONDS)

                val active = tables.runs().filter { it.sourceId == source.id }.single()
                val fire = tables.fires(schedule.id).single()
                assertEquals(active.id, fire.runId)
                val expected = if (manualRun == null) "run_created" to "schedule" else "skipped_active" to "manual"
                assertEquals(expected, fire.outcome to active.trigger)
            } finally {
                pool.shutdownNow()
            }
            tables.finishAll(time.plusSeconds(30))
        }
    }

    @Test
    fun `a schedule set again unchanged keeps its next fire, a changed one starts over from now`() {
        val source = source()
        schedule(source)
        clock.now = at("2026-09-30T10:59:00Z")

        val same = set(source, ScheduleDraft(HOURLY, "UTC", enabled = true))
        assertEquals(at("2026-09-30T11:00:00Z"), same.nextRunAt)

        val changed = set(source, ScheduleDraft("30 * * * *", "UTC", enabled = true))
        assertEquals(at("2026-09-30T11:30:00Z"), changed.nextRunAt)
        assertTrue(changed.updatedAt > same.createdAt)
    }
}
