// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.persistence.StepLogPartitions
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RunsTestConfiguration
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.micrometer.core.instrument.MeterRegistry
import org.junit.jupiter.api.Tag
import org.junit.jupiter.api.extension.ExtendWith
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val PASS_PATIENCE: Duration = Duration.ofSeconds(4)
private val LOGS_TERM: Duration = Duration.ofDays(30)

/** H1 phase 2: what the cleaning deletes by term, what it never deletes and in which order. */
@Tag("mutflow-isolated")
@MutFlowTest(
    includeTargets = [
        RetentionPass::class,
        StepLogRetention::class,
        RetentionTotals::class,
        MicrometerRetentionMetrics::class,
    ],
    // A mutant of the batch loop never ends: finishing() interrupts it and fails the test (a kill); mutflow's own
    // timeout, which would fail the whole run as "timed out", stays behind that as the last guard.
    timeoutMs = 20_000,
)
@ExtendWith(OutputCaptureExtension::class)
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = [
        "spring.grpc.server.port=0",
        "sard.scheduler.interval=1h",
        "sard.freshness.interval=1h",
        "sard.verification.interval=1h",
    ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class RetentionPassIntegrationTest(
    @Autowired private val retention: RetentionPass,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val logs: StepLogRetention,
    @Autowired private val logPartitions: StepLogPartitions,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val meters: MeterRegistry,
) {
    private val a = SceneTenant(jdbc)
    private val b = SceneTenant(jdbc)

    @BeforeTest
    fun `two tenants and no log partitions`() {
        clock.now = T0
        dropPartitions()
    }

    @AfterTest
    fun `drop the tenants and the partitions`() {
        dropPartitions()
        a.drop()
        b.drop()
        restorePartitions(logPartitions)
        clock.now = RUNS_NOW
    }

    private fun pass(): RetentionTotals = finishing { retention.run() }

    /**
     * Runs [block] as the mutated code under test and requires it to end by itself: the pass deletes in batches
     * until one is short, so a pass that is still going after [PASS_PATIENCE] never will end, and the batch loop
     * ends at once on an interrupted thread. A watchdog interrupts it and the test then fails.
     */
    private fun <T> finishing(block: () -> T): T {
        val worker = Thread.currentThread()
        val interrupted = AtomicBoolean(false)
        val watchdog =
            Executors.newSingleThreadScheduledExecutor { Thread(it, "pass-watchdog").apply { isDaemon = true } }
        watchdog.schedule({
            interrupted.set(true)
            worker.interrupt()
        }, PASS_PATIENCE.toMillis(), TimeUnit.MILLISECONDS)
        try {
            return MutFlow.underTest { block() }
        } finally {
            watchdog.shutdownNow()
            Thread.interrupted()
            assertFalse(interrupted.get(), "the pass did not end by itself within $PASS_PATIENCE")
        }
    }

    private fun dropPartitions() {
        for (name in jdbc.partitionNames()) jdbc.execute("drop table $name")
    }

    private fun partition(month: String) = jdbc.createPartition(month)

    private fun partitions(): Set<String> = jdbc.partitionNames().toSet()

    private fun counter(kind: String): Double =
        meters
            .get("sard.retention.deleted")
            .tag("kind", kind)
            .counter()
            .count()

    /**
     * A source of [owner] that protects nothing it is not asked to: a fresh success of each kind (backup with a
     * snapshot, restore, verify) and a fresh passed and a fresh not passed verification.
     */
    private fun anchored(owner: SceneTenant = a): UUID {
        val source = owner.source()
        val backup = owner.run(source, boundary(RUNS_TERM), status = "succeeded")
        val snapshot = owner.snapshot(backup.steps.single(), source)
        owner.run(source, boundary(RUNS_TERM), status = "succeeded", kind = "restore")
        val passed = owner.run(source, boundary(RUNS_TERM), status = "succeeded", trigger = "verification")
        val failed = owner.run(source, boundary(RUNS_TERM), status = "succeeded", trigger = "verification")
        owner.verification(source, snapshot, boundary(RUNS_TERM), "passed", passed)
        owner.verification(source, snapshot, boundary(RUNS_TERM), "failed", failed)
        return source
    }

    private fun run(
        owner: SceneTenant,
        row: UUID,
    ) = owner.exists("runs", row)

    // ---- terms -----------------------------------------------------------------------------------------------

    @Test
    fun `Старый запуск без снимка удаляется при любом итоге, свежий остаётся`() {
        val source = anchored()
        val old = listOf("failed", "cancelled", "succeeded").map { a.run(source, old(RUNS_TERM), status = it) }
        val fresh = a.run(source, boundary(RUNS_TERM))

        val totals = pass()

        assertTrue(old.none { run(a, it.id) })
        assertTrue(old.none { it.steps.any { step -> a.exists("run_steps", step) } })
        assertTrue(run(a, fresh.id), "a run exactly on the boundary stays")
        assertEquals(3, totals[RetentionKind.RUN])
        assertEquals(3, totals[RetentionKind.RUN_STEP])
    }

    @Test
    fun `Запуск удаляется вместе со всеми своими шагами`() {
        val source = anchored()
        val old = a.run(source, old(RUNS_TERM), steps = 3)

        pass()

        assertFalse(run(a, old.id))
        assertEquals(0, a.count("run_steps", "run_id = '${old.id}'"))
    }

    @Test
    fun `Старый запуск restore и verify без записи проверки удаляется`() {
        val source = anchored()
        val restore = a.run(source, old(RUNS_TERM), kind = "restore")
        val verify = a.run(source, old(RUNS_TERM), trigger = "verification")

        pass()

        assertFalse(run(a, restore.id))
        assertFalse(run(a, verify.id))
    }

    @Test
    fun `Активный запуск со старым созданием остаётся`() {
        val created = T0.minus(Duration.ofDays(400))
        val runs = listOf("queued", "dispatched", "running").map { a.run(a.source(), created, status = it) }

        pass()

        assertTrue(runs.all { run(a, it.id) && it.steps.all { s -> a.exists("run_steps", s) } })
    }

    @Test
    fun `Запись проверки удаляется вместе со своим закрытым заданием`() {
        val source = anchored()
        val snapshot = a.oldSnapshot(source)
        val oldRun = a.run(source, old(RUNS_TERM), trigger = "verification", status = "failed")
        val record = a.verification(source, snapshot, old(RUNS_TERM), "failed", oldRun)
        val job = jdbc.queryForObject("select job_id from restore_verifications where id = ?", UUID::class.java, record)

        val totals = pass()

        assertFalse(a.exists("restore_verifications", record))
        assertFalse(a.exists("verification_jobs", requireNotNull(job)))
        assertFalse(run(a, oldRun.id), "the verification run goes after its record, in the same pass")
        assertEquals(1, totals[RetentionKind.RESTORE_VERIFICATION])
        assertEquals(1, totals[RetentionKind.VERIFICATION_JOB])
    }

    @Test
    fun `Старая доставка в финальном статусе удаляется, в ожидании остаётся, свежая остаётся`() {
        val source = anchored()
        val run = a.run(source, boundary(RUNS_TERM))
        val gone =
            listOf("delivered", "skipped", "failed", "expired").mapIndexed { i, status ->
                a.delivery(run.id, old(DELIVERIES_TERM), status, "ch$i")
            }
        val pending = a.delivery(run.id, old(DELIVERIES_TERM), "pending", "waiting")
        val fresh = a.delivery(run.id, boundary(DELIVERIES_TERM), "delivered", "fresh")
        val young = a.delivery(run.id, T0.minus(Duration.ofDays(29)), "delivered", "young")

        val totals = pass()

        assertTrue(gone.none { a.exists("notification_deliveries", it) })
        assertTrue(listOf(pending, fresh, young).all { a.exists("notification_deliveries", it) })
        assertEquals(4, totals[RetentionKind.NOTIFICATION_DELIVERY])
    }

    @Test
    fun `Старая доставка о срабатывании расписания с алертом удаляется, срабатывание остаётся`() {
        val source = anchored()
        val schedule = a.schedule(source)
        val fire = a.fire(schedule, T0.minus(Duration.ofDays(20)), alert = true)
        a.fireDelivery(fire, old(DELIVERIES_TERM))

        pass()

        assertEquals(0, a.count("notification_deliveries", "fire_id = '$fire'"))
        assertTrue(a.exists("schedule_fires", fire))
    }

    @Test
    fun `Старая доставка об эпизоде просрочки и о событии сертификата удаляется, предмет остаётся`() {
        val source = anchored()
        val success = a.run(source, boundary(RUNS_TERM), status = "succeeded")
        val episode = a.overdueEpisode(source, success.id)
        val episodeDelivery = a.episodeDelivery(episode, old(DELIVERIES_TERM))
        val event = a.certificateEventDelivery(old(DELIVERIES_TERM))

        val totals = pass()

        assertFalse(a.exists("notification_deliveries", episodeDelivery))
        assertEquals(0, a.count("notification_deliveries", "certificate_event_id = '$event'"))
        assertTrue(a.exists("overdue_episodes", episode) && a.exists("agent_certificate_events", event))
        assertEquals(2, totals[RetentionKind.NOTIFICATION_DELIVERY])
    }

    // ---- snapshots and what keeps a run -----------------------------------------------------------------------

    @Test
    fun `Старый запуск со снимком остаётся при любом статусе, snapshots не меняется`() {
        val source = anchored()
        val kept =
            listOf("succeeded", "failed", "cancelled").map { status ->
                a
                    .run(
                        source,
                        old(RUNS_TERM),
                        status = status,
                    ).also { a.snapshot(it.steps.single(), source, forgotten = status == "failed") }
            }
        val before = jdbc.queryForList("select * from snapshots where tenant_id = ? order by id", a.id)

        pass()

        assertTrue(kept.all { run(a, it.id) && a.exists("run_steps", it.steps.single()) })
        assertEquals(before, jdbc.queryForList("select * from snapshots where tenant_id = ? order by id", a.id))
    }

    @Test
    fun `Запуск ждёт, пока на него ссылается живая запись`() {
        val source = anchored()
        val schedule = a.schedule(source)
        val withFire = a.run(source, old(RUNS_TERM))
        val withDelivery = a.run(source, old(RUNS_TERM))
        val withEpisode = a.run(source, old(RUNS_TERM), status = "succeeded")
        val withJob = a.run(source, old(RUNS_TERM), trigger = "verification", status = "failed")
        val withRecord = a.run(source, old(RUNS_TERM), trigger = "verification", status = "succeeded")
        val withLogs = a.run(source, old(RUNS_TERM))
        a.fire(schedule, T0.minus(Duration.ofDays(100)), run = withFire.id)
        a.delivery(withDelivery.id, T0.minus(Duration.ofDays(20)))
        a.overdueEpisode(source, withEpisode.id)
        val snapshot = a.oldSnapshot(source)
        a.job(source, snapshot, status = "running", run = withJob.id)
        a.verification(source, snapshot, T0.minus(Duration.ofDays(100)), "failed", withRecord)
        partition("2027_06")
        a.logLines(withLogs.steps.single(), 5, T0.minus(Duration.ofDays(1)))

        pass()

        for (kept in listOf(withFire, withDelivery, withEpisode, withJob, withRecord, withLogs)) {
            assertTrue(run(a, kept.id), "run ${kept.id} must wait")
        }
        assertEquals(5, a.count("step_logs"), "log lines are never deleted one by one")
    }

    @Test
    fun `Запуск удаляется в том же проходе, что и последняя ссылка на него`() {
        val source = anchored()
        val schedule = a.schedule(source)
        val run = a.run(source, old(RUNS_TERM))
        a.fire(schedule, old(RUNS_TERM), run = run.id)
        a.delivery(run.id, old(DELIVERIES_TERM))

        val totals = pass()

        assertFalse(run(a, run.id))
        assertEquals(1, totals[RetentionKind.SCHEDULE_FIRE])
        assertEquals(1, totals[RetentionKind.NOTIFICATION_DELIVERY])
    }

    // ---- last success ------------------------------------------------------------------------------------------

    @Test
    fun `Последний успех вида остаётся, более ранние удаляются`() {
        val source = a.source()
        val restores =
            (1L..3L).map {
                a.run(source, old(RUNS_TERM).minus(Duration.ofDays(it)), status = "succeeded", kind = "restore")
            }
        val verifies =
            (1L..3L).map {
                a.run(
                    source,
                    old(RUNS_TERM).minus(Duration.ofDays(it)),
                    status = "succeeded",
                    trigger = "verification",
                )
            }

        pass()

        assertEquals(listOf(true, false, false), restores.map { run(a, it.id) })
        assertEquals(listOf(true, false, false), verifies.map { run(a, it.id) })
    }

    @Test
    fun `Последний успех одного вида не защищает запуски другого вида`() {
        val source = a.source()
        val oldRestore = a.run(source, old(RUNS_TERM).minus(Duration.ofDays(5)), status = "succeeded", kind = "restore")
        a.run(source, old(RUNS_TERM), status = "succeeded", kind = "restore")
        val verify = a.run(source, old(RUNS_TERM), status = "succeeded", trigger = "verification")

        pass()

        assertFalse(run(a, oldRestore.id))
        assertTrue(run(a, verify.id))
    }

    @Test
    fun `Неуспешный запуск не считается последним успехом`() {
        val source = a.source()
        val runs = listOf("failed", "cancelled").map { a.run(source, old(RUNS_TERM), status = it, kind = "restore") }

        pass()

        assertTrue(runs.none { run(a, it.id) })
    }

    @Test
    fun `Последний успех считается по каждому источнику отдельно и для мягко удалённого источника`() {
        val runs = (1..5).map { a.run(a.source(), old(RUNS_TERM), status = "succeeded", kind = "restore") }
        val deleted = a.run(a.source(deleted = true), old(RUNS_TERM), status = "succeeded", kind = "restore")

        pass()

        assertTrue((runs + deleted).all { run(a, it.id) })
    }

    @Test
    fun `Последний успех в одном тенанте не защищает запуски другого`() {
        val sa = a.source()
        val sb = b.source()
        val onlyA = a.run(sa, old(RUNS_TERM), status = "succeeded", kind = "restore")
        val earlierB = b.run(sb, old(RUNS_TERM).minus(Duration.ofDays(3)), status = "succeeded", kind = "restore")
        val laterB = b.run(sb, old(RUNS_TERM), status = "succeeded", kind = "restore")

        pass()

        assertTrue(run(a, onlyA.id))
        assertFalse(run(b, earlierB.id))
        assertTrue(run(b, laterB.id))
    }

    // ---- verification records ---------------------------------------------------------------------------------

    private fun verifiedSource(): Pair<UUID, UUID> {
        val source = a.source()
        val snapshot = a.oldSnapshot(source)
        return source to snapshot
    }

    @Test
    fun `Последняя успешная проверка источника остаётся`() {
        val (source, snapshot) = verifiedSource()
        val records =
            (1L..4L).map { a.verification(source, snapshot, old(RUNS_TERM).minus(Duration.ofDays(it)), "passed") }

        pass()

        assertEquals(listOf(true, false, false, false), records.map { a.exists("restore_verifications", it) })
    }

    @Test
    fun `Последняя неуспешная проверка источника остаётся`() {
        val (source, snapshot) = verifiedSource()
        val failed = a.verification(source, snapshot, old(RUNS_TERM).minus(Duration.ofDays(30)), "failed")
        val cannot = a.verification(source, snapshot, old(RUNS_TERM).minus(Duration.ofDays(20)), "cannot_verify")
        val none = a.verification(source, snapshot, old(RUNS_TERM).minus(Duration.ofDays(10)), "no_verifier")
        a.verification(source, snapshot, boundary(RUNS_TERM), "passed")

        pass()

        assertTrue(a.exists("restore_verifications", none))
        assertFalse(a.exists("restore_verifications", failed))
        assertFalse(a.exists("restore_verifications", cannot))
    }

    @Test
    fun `Последние проверки считаются по каждому источнику отдельно`() {
        val records =
            (1..4).flatMap {
                val (source, snapshot) = verifiedSource()
                listOf(
                    a.verification(source, snapshot, old(RUNS_TERM), "passed"),
                    a.verification(source, snapshot, old(RUNS_TERM), "failed"),
                )
            }

        pass()

        assertTrue(records.all { a.exists("restore_verifications", it) })
    }

    @Test
    fun `Проверка не удаляется, пока её задание открыто`() {
        val (source, snapshot) = verifiedSource()
        val record = a.verification(source, snapshot, old(RUNS_TERM), "failed", jobStatus = "running")
        a.verification(source, snapshot, boundary(RUNS_TERM), "failed")
        a.verification(source, snapshot, boundary(RUNS_TERM), "passed")

        pass()

        assertTrue(a.exists("restore_verifications", record))
    }

    @Test
    fun `Закрытый эпизод устаревания удаляется перед проверкой, которая его закрыла`() {
        val (source, snapshot) = verifiedSource()
        val closing = a.verification(source, snapshot, old(RUNS_TERM).minus(Duration.ofDays(1)), "passed")
        a.verification(source, snapshot, old(RUNS_TERM), "passed")
        val episode = a.staleEpisode(source, closing)

        pass()

        assertFalse(a.exists("verification_stale_episodes", episode))
        assertFalse(a.exists("restore_verifications", closing))
    }

    @Test
    fun `Эпизод устаревания, закрытый последней проверкой источника, остаётся, открытый тоже`() {
        val (source, snapshot) = verifiedSource()
        val lastPassed = a.verification(source, snapshot, old(RUNS_TERM), "passed")
        val lastOther = a.verification(source, snapshot, old(RUNS_TERM), "no_verifier")
        val byPassed = a.staleEpisode(source, lastPassed)
        val byOther = a.staleEpisode(source, lastOther)
        val open = a.staleEpisode(a.source(), null, T0.minus(Duration.ofDays(400)))

        pass()

        for (kept in listOf(byPassed, byOther)) assertTrue(a.exists("verification_stale_episodes", kept))
        assertTrue(a.exists("restore_verifications", lastPassed) && a.exists("restore_verifications", lastOther))
        assertTrue(a.exists("verification_stale_episodes", open))
    }

    // ---- schedule fires ---------------------------------------------------------------------------------------

    @Test
    fun `Срабатывание старше срока удаляется, свежее остаётся`() {
        val schedule = a.schedule(anchored())
        val old = (1..3).map { a.fire(schedule, old(RUNS_TERM)) }
        val fresh = a.fire(schedule, boundary(RUNS_TERM))

        val totals = pass()

        assertTrue(old.none { a.exists("schedule_fires", it) })
        assertTrue(a.exists("schedule_fires", fresh))
        assertEquals(3, totals[RetentionKind.SCHEDULE_FIRE])
    }

    @Test
    fun `Строки простоя удаляются раньше своего догоняющего срабатывания`() {
        val schedule = a.schedule(anchored())
        val catchUp = a.fire(schedule, old(RUNS_TERM), kind = "catch_up")
        val downtimes = (1..3).map { a.fire(schedule, old(RUNS_TERM).minusSeconds(100), catchUpOf = catchUp) }

        pass()

        assertFalse(a.exists("schedule_fires", catchUp))
        assertTrue(downtimes.none { a.exists("schedule_fires", it) })
    }

    @Test
    fun `Старое догоняющее срабатывание ждёт свежей строки простоя`() {
        val schedule = a.schedule(anchored())
        val catchUp = a.fire(schedule, old(RUNS_TERM), kind = "catch_up")
        val downtime = a.fire(schedule, boundary(RUNS_TERM), catchUpOf = catchUp)

        pass()

        assertTrue(a.exists("schedule_fires", catchUp) && a.exists("schedule_fires", downtime))
    }

    @Test
    fun `Срабатывание с алертом ждёт своей доставки`() {
        val schedule = a.schedule(anchored())
        val fire = a.fire(schedule, old(RUNS_TERM), alert = true)
        val delivery = a.delivery(a.run(anchored(), boundary(RUNS_TERM)).id, boundary(DELIVERIES_TERM))
        jdbc.update("update notification_deliveries set run_id = null, fire_id = ? where id = ?", fire, delivery)

        pass()

        assertTrue(a.exists("schedule_fires", fire) && a.exists("notification_deliveries", delivery))
    }

    // ---- log partitions ---------------------------------------------------------------------------------------

    private fun lines(month: String) = jdbc.queryForObject("select count(*) from step_logs_$month", Int::class.java)

    private fun stepWithLines(
        months: Map<String, Instant>,
        owner: SceneTenant = a,
    ): UUID {
        val step = owner.run(owner.source(), boundary(RUNS_TERM), status = "succeeded").steps.single()
        for ((month, at) in months) {
            partition(month)
            owner.logLines(step, 1000, at)
        }
        return step
    }

    @Test
    fun `Секция удаляется, когда её верхняя граница совпала с границей срока`() {
        clock.now = Instant.parse("2027-03-31T00:00:00Z")
        stepWithLines(
            mapOf(
                "2027_01" to Instant.parse("2027-01-15T00:00:00Z"),
                "2027_02" to Instant.parse("2027-02-15T00:00:00Z"),
                "2027_03" to Instant.parse("2027-03-15T00:00:00Z"),
            ),
        )
        partition("2027_04")

        val totals = pass()

        assertEquals(setOf("step_logs_2027_03", "step_logs_2027_04"), partitions())
        assertEquals(1000, lines("2027_03"))
        assertEquals(2, totals[RetentionKind.STEP_LOG_PARTITION])
        assertEquals(2.0, counter("step_log_partition"))
    }

    @Test
    fun `Секция остаётся за секунду до своей границы`() {
        clock.now = Instant.parse("2027-03-30T23:59:59Z")
        stepWithLines(mapOf("2027_02" to Instant.parse("2027-02-15T00:00:00Z")))

        pass()

        assertEquals(1000, lines("2027_02"))
    }

    @Test
    fun `Текущая и будущие секции не удаляются даже при сроке в один день`() {
        clock.now = Instant.parse("2027-03-02T00:00:00Z")
        listOf("2027_02", "2027_03", "2027_04", "2027_05").forEach(::partition)
        val short = RetentionPass(sessions, logs, clock, settings(stepLogs = Duration.ofDays(1)), { _, _ -> })

        short.run()

        assertEquals(setOf("step_logs_2027_03", "step_logs_2027_04", "step_logs_2027_05"), partitions())
    }

    @Test
    fun `Удаление секции общее для всех тенантов`() {
        clock.now = Instant.parse("2027-03-31T00:00:00Z")
        val january = mapOf("2027_01" to Instant.parse("2027-01-15T00:00:00Z"))
        stepWithLines(january, a)
        stepWithLines(january, b)
        val before = counter("step_log_partition")

        pass()

        assertEquals(emptySet(), partitions())
        assertEquals(before + 1, counter("step_log_partition"))
    }

    @Test
    fun `Запуск удаляется в том же проходе, что и секция его логов`() {
        val short = RetentionPass(sessions, logs, clock, settings(runs = Duration.ofDays(10)), { _, _ -> })
        val source = anchoredShort()
        val run = a.run(source, T0.minus(Duration.ofDays(120)))
        partition("2027_01")
        a.logLines(run.steps.single(), 10, Instant.parse("2027-01-15T00:00:00Z"))

        short.run()

        assertEquals(emptySet(), partitions())
        assertFalse(run(a, run.id))
    }

    private fun anchoredShort(): UUID {
        val source = a.source()
        a.run(source, T0, status = "succeeded")
        return source
    }

    @Test
    fun `Запуск со строками лога в живой секции ждёт удаления секции, без ошибки`(output: CapturedOutput) {
        val short = RetentionPass(sessions, logs, clock, settings(runs = Duration.ofDays(10)), { _, _ -> })
        val run = a.run(anchoredShort(), T0.minus(Duration.ofDays(15)))
        partition("2027_06")
        a.logLines(run.steps.single(), 1000, T0.minus(Duration.ofDays(1)))

        short.run()

        assertTrue(run(a, run.id))
        assertEquals(1000, lines("2027_06"))
        assertFalse("ERROR" in output.out)
    }

    @Test
    fun `Ошибка удаления секции не мешает удалению строк и не пишет текст строк лога`(output: CapturedOutput) {
        clock.now = Instant.parse("2027-03-31T00:00:00Z")
        val old = a.runs(a.source(), 3, Instant.parse("2026-01-01T00:00:00Z"))
        partition("2027_01")
        a.logLines(a.run(a.source(), boundary(RUNS_TERM)).steps.single(), 3, Instant.parse("2027-01-15T00:00:00Z"))
        jdbc.update("update step_logs set text = 'secret log text' where tenant_id = ?", a.id)
        val holder = jdbc.dataSource!!.connection
        try {
            holder.autoCommit = false
            // An open transaction that has read the partition: the drop needs it to itself and waits.
            holder.createStatement().use { it.execute("select count(*) from step_logs_2027_01") }
            val quick = StepLogRetention(jdbc, Duration.ofMillis(200))
            val impatient = RetentionPass(sessions, quick, clock, settings()) { _, _ -> }

            finishing { impatient.run() }
            val errors = output.out.lines().filter { "ERROR" in it && "Retention" in it }
            assertTrue(
                errors.any { "step_logs_2027_01" in it && "Exception" in it && "lock_timeout" in it },
                errors.toString(),
            )
            assertTrue("step_logs_2027_01" in partitions())
            holder.rollback()
            impatient.run()
            assertFalse("step_logs_2027_01" in partitions(), "the next pass drops the partition")
        } finally {
            holder.rollback()
            holder.close()
        }

        assertFalse("secret log text" in output.out)
        assertTrue(old.none { run(a, it.id) })
    }

    // ---- tenants, writes and reports -------------------------------------------------------------------------

    @Test
    fun `Один проход убирает строки каждого тенанта, ссылка в одном тенанте не держит запуск другого`() {
        val oldA = a.run(anchored(a), old(RUNS_TERM))
        val oldB = b.run(anchored(b), old(RUNS_TERM))
        a.delivery(a.run(a.source(), boundary(RUNS_TERM)).id, boundary(DELIVERIES_TERM))

        pass()

        assertFalse(run(a, oldA.id))
        assertFalse(run(b, oldB.id))
    }

    @Test
    fun `Проход пишет одну строку итога с числом удалённого по каждому типу`(output: CapturedOutput) {
        val source = anchored()
        val schedule = a.schedule(source)
        a.runs(source, 5, old(RUNS_TERM), steps = 2)
        a.fires(schedule, 3, old(RUNS_TERM))
        a.deliveries(source, 4, old(DELIVERIES_TERM))
        val (vSource, snapshot) = verifiedSource()
        a.verification(vSource, snapshot, old(RUNS_TERM), "passed")
        a.verifications(vSource, snapshot, 4, old(RUNS_TERM).minus(Duration.ofDays(1)), "passed")
        partition("2027_01")
        partition("2027_02")

        pass()

        val summaries = output.out.lines().filter { "Retention pass deleted" in it }
        assertEquals(1, summaries.size, output.out)
        assertTrue(summaries.single().contains("INFO"))
        assertEquals(
            "Retention pass deleted step_log_partition=2 notification_delivery=4 schedule_fire=3 " +
                "restore_verification=4 verification_job=4 run_step=10 run=5",
            summaries.single().substringAfter("Retention pass").let { "Retention pass" + it },
        )
    }

    @Test
    fun `Пустая база даёт проход без удалений`(output: CapturedOutput) {
        val before = RetentionKind.entries.associateWith { counter(it.tag) }

        pass()

        val line = output.out.lines().single { "Retention pass deleted" in it }
        assertTrue(
            line.endsWith(
                "step_log_partition=0 notification_delivery=0 schedule_fire=0 restore_verification=0 " +
                    "verification_job=0 run_step=0 run=0",
            ),
            line,
        )
        assertEquals(before, RetentionKind.entries.associateWith { counter(it.tag) })
    }

    private fun settings(
        stepLogs: Duration = LOGS_TERM,
        runs: Duration = RUNS_TERM,
    ) = RetentionSettings(stepLogs, runs, RUNS_TERM, RUNS_TERM, DELIVERIES_TERM, Duration.ofHours(1), 500)
}
