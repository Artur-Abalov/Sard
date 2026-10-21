// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import dev.sard.server.api.RestApiTest
import dev.sard.server.persistence.StepLogPartitions
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.LogLine
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.Runs
import dev.sard.server.runs.StepLogs
import io.micrometer.core.instrument.simple.SimpleMeterRegistry
import org.junit.jupiter.api.extension.ExtendWith
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val LOGS_TERM: Duration = Duration.ofDays(30)

/** The same cleaning at the sizes of the specification: whole batches, two servers, a held row, an open transaction. */
@ExtendWith(OutputCaptureExtension::class)
@RestApiTest
class RetentionVolumeIntegrationTest(
    @Autowired private val sessions: TenantSessions,
    @Autowired private val logs: StepLogRetention,
    @Autowired private val logPartitions: StepLogPartitions,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val runs: Runs,
    @Autowired private val stepLogs: StepLogs,
) {
    private val a = SceneTenant(jdbc)
    private val b = SceneTenant(jdbc)

    /** What a pass told per batch, in order. */
    private class Batches : RetentionMetrics {
        val told = CopyOnWriteArrayList<Pair<RetentionKind, Int>>()

        override fun deleted(
            kind: RetentionKind,
            count: Int,
        ) {
            told += kind to count
        }

        fun of(kind: RetentionKind) = told.filter { it.first == kind }.map { it.second }
    }

    @BeforeTest
    fun `the moment of the pass`() {
        clock.now = T0
    }

    @AfterTest
    fun `drop the tenants`() {
        a.drop()
        b.drop()
        restorePartitions(logPartitions)
        clock.now = RUNS_NOW
    }

    private fun pass(
        metrics: RetentionMetrics = RetentionMetrics { _, _ -> },
        batchSize: Int = 500,
    ) = RetentionPass(
        sessions,
        logs,
        clock,
        RetentionSettings(LOGS_TERM, RUNS_TERM, RUNS_TERM, RUNS_TERM, DELIVERIES_TERM, Duration.ofHours(1), batchSize),
        metrics,
    )

    private fun count(
        owner: SceneTenant,
        table: String,
        where: String = "true",
    ) = owner.count(table, where)

    @Test
    fun `Старые строки каждого типа удаляются, свежие остаются`() {
        val source = a.source()
        val anchor = a.run(source, boundary(RUNS_TERM), status = "succeeded")
        val snapshot = a.snapshot(anchor.steps.single(), source)
        a.runs(source, 700, old(RUNS_TERM))
        a.runs(source, 700, boundary(RUNS_TERM))
        a.fires(a.schedule(source), 700, old(RUNS_TERM))
        a.fires(a.schedule(a.source()), 700, boundary(RUNS_TERM))
        a.deliveries(source, 700, old(DELIVERIES_TERM))
        a.deliveries(source, 700, boundary(DELIVERIES_TERM))
        a.verifications(source, snapshot, 700, old(RUNS_TERM), "failed")
        a.verifications(source, snapshot, 700, boundary(RUNS_TERM), "failed")
        a.verifications(source, snapshot, 1, boundary(RUNS_TERM), "passed")

        val totals = pass().run()

        assertEquals(700, totals[RetentionKind.SCHEDULE_FIRE].toInt())
        assertEquals(700, totals[RetentionKind.NOTIFICATION_DELIVERY].toInt())
        assertEquals(700, totals[RetentionKind.RESTORE_VERIFICATION].toInt())
        assertEquals(700, totals[RetentionKind.VERIFICATION_JOB].toInt())
        assertEquals(700, count(a, "schedule_fires", "recorded_at = '${boundary(RUNS_TERM)}'"))
        assertEquals(701, count(a, "restore_verifications"))
        assertEquals(0, count(a, "restore_verifications", "verified_at < '${boundary(RUNS_TERM)}'"))
        assertEquals(0, count(a, "notification_deliveries", "created_at < '${boundary(DELIVERIES_TERM)}'"))
        assertEquals(700, count(a, "notification_deliveries"))
        assertEquals(0, count(a, "runs", "finished_at < '${boundary(RUNS_TERM)}' and status = 'failed'"))
        assertEquals(700, count(a, "runs", "status = 'failed' and finished_at = '${boundary(RUNS_TERM)}'"))
    }

    @Test
    fun `Больше пачки удаляется за один проход несколькими транзакциями`() {
        val source = a.source()
        a.runs(source, 1201, old(RUNS_TERM), steps = 2)
        val batches = Batches()

        pass(batches).run()

        assertEquals(0, count(a, "runs"))
        assertEquals(listOf(500, 500, 201), batches.of(RetentionKind.RUN))
        assertEquals(listOf(1000, 1000, 402), batches.of(RetentionKind.RUN_STEP))
    }

    @Test
    fun `Размер пачки берётся из свойства`() {
        a.runs(a.source(), 250, old(RUNS_TERM))
        val batches = Batches()

        pass(batches, batchSize = 100).run()

        assertEquals(0, count(a, "runs"))
        assertEquals(listOf(100, 100, 50), batches.of(RetentionKind.RUN))
    }

    @Test
    fun `Прерванный проход дочищается следующим проходом`(output: CapturedOutput) {
        a.runs(a.source(), 1201, old(RUNS_TERM))
        val failing = RetentionMetrics { kind, _ -> if (kind == RetentionKind.RUN) error("the database went away") }

        pass(failing).run()

        assertEquals(701, count(a, "runs"), "the batch committed before the failure stays deleted")
        assertTrue(output.out.lines().any { "ERROR" in it && "IllegalStateException" in it })

        pass().run()

        assertEquals(0, count(a, "runs"))
    }

    @Test
    fun `Остановка после первой пачки не оставляет частичной пачки`() {
        a.runs(a.source(), 1201, old(RUNS_TERM), steps = 2)
        var runBatches = 0
        val stopping =
            RetentionMetrics { kind, _ ->
                if (kind == RetentionKind.RUN && ++runBatches == 1) Thread.currentThread().interrupt()
            }

        try {
            pass(stopping).run()
        } finally {
            Thread.interrupted()
        }

        assertEquals(701, count(a, "runs"), "the stopped pass left the second batch whole, not half of it")
        val stepless = "not exists (select 1 from run_steps s where s.run_id = r.id and s.tenant_id = r.tenant_id)"
        assertEquals(0, count(a, "runs r", stepless))
        assertEquals(1402, count(a, "run_steps"))
    }

    @Test
    fun `Одна строка итога на проход и ни одной ошибки при двух экземплярах`(output: CapturedOutput) {
        val source = a.source()
        a.runs(source, 5000, old(RUNS_TERM))
        a.deliveries(source, 5000, old(DELIVERIES_TERM))
        val meters = listOf(SimpleMeterRegistry(), SimpleMeterRegistry())
        val instances = meters.map { pass(MicrometerRetentionMetrics(it)) }
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(2)

        val done =
            instances.map {
                pool.submit {
                    start.await()
                    it.run()
                }
            }
        start.countDown()
        done.forEach { it.get(300, TimeUnit.SECONDS) }
        pool.shutdown()

        fun sum(kind: String) =
            meters.sumOf {
                it
                    .get("sard.retention.deleted")
                    .tag("kind", kind)
                    .counter()
                    .count()
            }
        assertEquals(0, count(a, "runs", "finished_at < '${boundary(RUNS_TERM)}'"))
        assertEquals(0, count(a, "notification_deliveries"))
        assertEquals(5000.0, sum("run"))
        assertEquals(5000.0, sum("notification_delivery"))
        assertFalse(output.out.lines().any { "ERROR" in it && "Retention" in it }, "no instance writes an error")
    }

    @Test
    fun `Два экземпляра не удаляют одну секцию дважды`(output: CapturedOutput) {
        jdbc.execute("drop table if exists step_logs_2027_01")
        jdbc.createPartition("2027_01")
        val before =
            jdbc.partitionNames()
        val meters = listOf(SimpleMeterRegistry(), SimpleMeterRegistry())
        val instances = meters.map { pass(MicrometerRetentionMetrics(it)) }
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(2)

        val done =
            instances.map {
                pool.submit {
                    start.await()
                    it.run()
                }
            }
        start.countDown()
        done.forEach { it.get(120, TimeUnit.SECONDS) }
        pool.shutdown()

        val left =
            jdbc.partitionNames()
        assertTrue("step_logs_2027_01" in before && "step_logs_2027_01" !in left)
        val dropped =
            meters.sumOf {
                it
                    .get("sard.retention.deleted")
                    .tag("kind", "step_log_partition")
                    .counter()
                    .count()
            }
        assertEquals(before.size - left.size, dropped.toInt(), "every partition is counted once")
        assertFalse(output.out.lines().any { "ERROR" in it && "Retention" in it })
    }

    @Test
    fun `Один проход убирает строки каждого тенанта`() {
        a.runs(a.source(), 700, old(RUNS_TERM))
        b.runs(b.source(), 700, old(RUNS_TERM))

        pass().run()

        assertEquals(0, count(a, "runs"))
        assertEquals(0, count(b, "runs"))
    }

    @Test
    fun `Строка, заблокированная другой транзакцией, пропускается без ожидания`() {
        val source = a.source()
        val held = a.run(source, old(RUNS_TERM))
        val others = a.runs(source, 10, old(RUNS_TERM))
        val holder = jdbc.dataSource!!.connection
        try {
            holder.autoCommit = false
            holder.createStatement().use { it.execute("select 1 from runs where id = '${held.id}' for update") }

            val finished = Executors.newSingleThreadExecutor().submit<RetentionTotals> { pass().run() }

            val totals = finished.get(60, TimeUnit.SECONDS)
            assertEquals(10, totals[RetentionKind.RUN])
        } finally {
            holder.rollback()
            holder.close()
        }
        assertTrue(a.exists("runs", held.id))
        assertTrue(others.none { a.exists("runs", it.id) })
    }

    @Test
    fun `Создание запуска не ждёт пачки уборки`() {
        val source = a.source()
        a.runs(source, 500, old(RUNS_TERM))
        val holder = jdbc.dataSource!!.connection
        val pool = Executors.newSingleThreadExecutor()
        try {
            holder.autoCommit = false
            // The batch of the cleaning, left open: it has deleted 500 old runs of the source and not committed.
            holder.createStatement().use {
                it.executeUpdate("delete from run_steps where tenant_id = '${a.id}' and source_id = '$source'")
                it.executeUpdate("delete from runs where tenant_id = '${a.id}' and source_id = '$source'")
            }

            val started = pool.submit<Boolean> { runs.start(a.id, source).let { true } }

            assertTrue(started.get(30, TimeUnit.SECONDS), "the run was created while the batch was open")
        } finally {
            holder.rollback()
            holder.close()
            pool.shutdown()
        }
    }

    @Test
    fun `Запуск, завершившийся во время прохода, не удаляется этим проходом`() {
        val source = a.source()
        a.runs(source, 600, old(RUNS_TERM))
        val active = a.run(source, T0.minus(Duration.ofDays(400)), status = "running")
        val finishing =
            RetentionMetrics { kind, _ ->
                if (kind == RetentionKind.RUN) {
                    jdbc.update(
                        "update run_steps set status = 'failed', finished_at = ? where run_id = ?",
                        stamp(T0.minusSeconds(1)),
                        active.id,
                    )
                    val at = stamp(T0.minusSeconds(1))
                    jdbc.update("update runs set status = 'failed', finished_at = ? where id = ?", at, active.id)
                }
            }

        pass(finishing).run()

        assertTrue(a.exists("runs", active.id))
    }

    /**
     * The drop of a partition needs the parent `step_logs` to itself while it waits for a reader of the partition, so
     * the lines of every tenant wait with it, for at most the lock timeout (decision F2, variant b).
     */
    @Test
    fun `Запись строки лога во время ожидающего удаления секции ждёт не дольше lock_timeout`() {
        jdbc.createPartition("2027_01")
        jdbc.createPartition("2027_06")
        val source = a.source()
        val running = a.run(source, T0, status = "running")
        val step = running.steps.single()
        val lockTimeout = Duration.ofSeconds(2)
        val reader = jdbc.dataSource!!.connection
        val pool = Executors.newFixedThreadPool(2)
        try {
            reader.autoCommit = false
            reader.createStatement().use { it.execute("select count(*) from step_logs_2027_01") }
            val impatient = StepLogRetention(jdbc, lockTimeout)
            val drop = pool.submit<Result<Boolean>> { runCatching { impatient.drop("step_logs_2027_01") } }
            waitForPendingLock()

            val started = System.nanoTime()
            val written = stepLogs.append(a.id, a.agentId, step, listOf(LogLine(null, "info", "while the drop waits")))
            val appendMs = Duration.ofNanos(System.nanoTime() - started).toMillis()
            val other = a.source("other")
            val startedRun = System.nanoTime()
            runs.start(a.id, other)
            val runMs = Duration.ofNanos(System.nanoTime() - startedRun).toMillis()
            val dropped = drop.get(30, TimeUnit.SECONDS)

            println("RETENTION F2: append $appendMs ms, run $runMs ms, drop failed=${dropped.isFailure}")
            assertTrue(written != null, "the line was written")
            assertTrue(appendMs <= lockTimeout.toMillis() + 1000, "the write waited $appendMs ms")
            assertTrue(runMs <= lockTimeout.toMillis(), "run creation waited $runMs ms")
            assertTrue(dropped.isFailure, "the drop gave up at lock_timeout")
            assertEquals(1, count(a, "step_logs", "step_id = '$step'"))
            val stepStatus = jdbc.queryForObject("select status from run_steps where id = ?", String::class.java, step)
            val runStatus = jdbc.queryForObject("select status from runs where id = ?", String::class.java, running.id)
            assertEquals("running", stepStatus)
            assertEquals("running", runStatus)
        } finally {
            reader.rollback()
            reader.close()
            pool.shutdown()
        }
    }

    private fun waitForPendingLock() {
        val deadline = System.nanoTime() + Duration.ofSeconds(5).toNanos()
        while (System.nanoTime() < deadline) {
            val waiting = jdbc.queryForObject("select count(*) from pg_locks where not granted", Int::class.java) ?: 0
            if (waiting > 0) return
            Thread.sleep(20)
        }
        error("the drop never started to wait for a lock")
    }
}
