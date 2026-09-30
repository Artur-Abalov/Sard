// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.persistence.StepLogPartitions
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.MovableClock
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
import kotlin.test.assertNull

private val LATER: Duration = Duration.ofSeconds(9)
private const val LINES = "select seq, level, text, time, received_at from step_logs where step_id = ? order by seq"
private val AGENT_TIME: Instant = Instant.parse("2026-09-30T09:59:58Z")

/** StepProgress and LogChunk as the domain records them (S7a tests 4 and 5, database side). */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class ProgressAndLogsIntegrationTest(
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val steps: StepTransitions,
    @Autowired private val progress: StepProgressWrites,
    @Autowired private val logs: StepLogs,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val partitions: StepLogPartitions,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc)
    private val other = RunsTenant(jdbc)

    @BeforeTest
    fun `two tenants with an agent each`() {
        clock.now = RUNS_NOW
        tenant.create()
        other.create()
    }

    @AfterTest
    fun `drop both tenants`() {
        for (t in listOf(tenant, other)) jdbc.update("delete from step_logs where tenant_id = ?", t.id)
        tenant.drop()
        other.drop()
    }

    private fun dispatched(): Pair<RunView, UUID> {
        val run = runs.start(tenant.id, sources.create(tenant.id, tenant.draft()).id)
        val step = run.steps.single().id
        steps.claim(tenant.id, step)
        clock.now = RUNS_NOW + LATER
        return run to step
    }

    private fun row(
        table: String,
        id: UUID,
    ): Map<String, Any?> = jdbc.queryForMap("select * from $table where id = ?", id)

    private fun instant(value: Any?): Instant? = (value as Timestamp?)?.toInstant()

    private fun write(
        step: UUID,
        report: ProgressReport,
        agent: UUID = tenant.agentId,
        tenantId: UUID = tenant.id,
    ) = progress.write(tenantId, agent, step, report)

    private fun lines(step: UUID): List<List<Any?>> =
        jdbc
            .queryForList(LINES, step)
            .map { listOf(it["seq"], it["level"], it["text"], instant(it["time"]), instant(it["received_at"])) }

    private fun counters(step: UUID): List<Any?> {
        val row = row("run_steps", step)
        return listOf(row["log_lines"], row["log_bytes"], row["log_truncated"])
    }

    // --- 4: progress

    @Test
    fun `the first progress starts a dispatched step and its run`() {
        val (run, step) = dispatched()

        assertEquals(ProgressWritten.STARTED, write(step, ProgressReport("dumping", 10, 100)))

        val stepRow = row("run_steps", step)
        assertEquals(
            listOf<Any?>("running", "dumping", 10L, 100L, clock.now),
            listOf("status", "phase", "bytes_processed", "bytes_total").map { stepRow[it] } +
                instant(stepRow["started_at"]),
        )
        val runRow = row("runs", run.id)
        assertEquals(listOf<Any?>("running", clock.now), listOf(runRow["status"], instant(runRow["started_at"])))
    }

    @Test
    fun `later progress moves the phase and counters, and an unknown value keeps the last one`() {
        val (_, step) = dispatched()
        write(step, ProgressReport("dumping", 10, 100))

        assertEquals(ProgressWritten.UPDATED, write(step, ProgressReport("uploading", 60, null)))
        assertEquals(ProgressWritten.UPDATED, write(step, ProgressReport(null, 70, null)))

        val stepRow = row("run_steps", step)
        assertEquals(
            listOf<Any?>("uploading", 70L, 100L, RUNS_NOW + LATER),
            listOf("phase", "bytes_processed", "bytes_total").map { stepRow[it] } + instant(stepRow["started_at"]),
        )
    }

    @Test
    fun `progress of a closed or queued step changes nothing`() {
        val (_, step) = dispatched()
        steps.finished(tenant.id, step, StepOutcome(StepState.SUCCEEDED, null))
        val closed = row("run_steps", step)
        val queued =
            runs
                .start(tenant.id, sources.create(tenant.id, tenant.draft("second")).id)
                .steps
                .single()
                .id

        assertEquals(ProgressWritten.CLOSED, write(step, ProgressReport("verifying", 1, 1)))
        assertEquals(ProgressWritten.CLOSED, write(queued, ProgressReport("dumping", 1, 1)))

        assertEquals(closed, row("run_steps", step))
        assertEquals("queued", row("run_steps", queued)["status"])
    }

    @Test
    fun `progress for a step of another agent or tenant is unknown and changes nothing`() {
        val (_, step) = dispatched()
        val sibling = UUID.randomUUID()
        tenant.insertAgent(sibling)

        assertEquals(ProgressWritten.UNKNOWN, write(step, ProgressReport("dumping", 1, 1), agent = sibling))
        val foreign = write(step, ProgressReport("dumping", 1, 1), agent = other.agentId, tenantId = other.id)
        assertEquals(ProgressWritten.UNKNOWN, foreign)

        assertEquals("dispatched", row("run_steps", step)["status"])
    }

    // --- 5: logs

    @Test
    fun `lines get the server's sequence in order across chunks`() {
        val (_, step) = dispatched()
        val first = listOf(LogLine(AGENT_TIME, "info", "one"), LogLine(AGENT_TIME, "warn", "two"))
        val second = listOf(LogLine(null, "error", "three"))

        val appended = logs.append(tenant.id, tenant.agentId, step, first)
        assertEquals(Appended(kept = 2, dropped = 0, marked = false), appended)
        clock.now = clock.now + LATER
        logs.append(tenant.id, tenant.agentId, step, second)

        val at = RUNS_NOW + LATER
        assertEquals(
            listOf(
                listOf<Any?>(1L, "info", "one", AGENT_TIME, at),
                listOf<Any?>(2L, "warn", "two", AGENT_TIME, at),
                listOf<Any?>(3L, "error", "three", null, at + LATER),
            ),
            lines(step),
        )
        assertEquals(listOf<Any?>(3L, 11L, false), counters(step))
    }

    @Test
    fun `past the step's limit one mark is written and every later line is dropped`() {
        val (_, step) = dispatched()
        val small = StepLogs(sessions, clock, LogLimits(maxLineBytes = 10, maxStepBytes = 8))
        val chunk = listOf("abc", "defg", "hij", "k").map { LogLine(AGENT_TIME, "info", it) }

        val appended = small.append(tenant.id, tenant.agentId, step, chunk)
        assertEquals(Appended(kept = 2, dropped = 2, marked = true), appended)
        val later = small.append(tenant.id, tenant.agentId, step, listOf(LogLine(AGENT_TIME, "info", "l")))

        assertEquals(Appended(kept = 0, dropped = 1, marked = false), later)
        val mark = LogBudget.mark(LogLimits(10, 8))
        assertEquals(listOf("abc", "defg", mark.text), lines(step).map { it[2] })
        assertEquals(listOf<Any?>(3L, 7L, true), counters(step))
    }

    @Test
    fun `lines of a closed step are kept, since the agent sends them after its result`() {
        val (_, step) = dispatched()
        steps.finished(tenant.id, step, StepOutcome(StepState.FAILED, "disk full"))

        logs.append(tenant.id, tenant.agentId, step, listOf(LogLine(AGENT_TIME, "error", "no space left")))

        assertEquals(listOf("no space left"), lines(step).map { it[2] })
    }

    @Test
    fun `lines for a step of another agent or tenant are not written`() {
        val (_, step) = dispatched()
        val line = listOf(LogLine(AGENT_TIME, "info", "x"))

        assertNull(logs.append(other.id, other.agentId, step, line))
        assertNull(logs.append(tenant.id, UUID.randomUUID(), step, line))

        assertEquals(emptyList(), lines(step))
        assertEquals(listOf<Any?>(0L, 0L, false), counters(step))
    }

    @Test
    fun `partitions are created ahead, once`() {
        val march = Instant.parse("2031-03-15T12:00:00Z")
        try {
            val created = partitions.ensure(march, monthsAhead = 2)

            assertEquals(listOf("step_logs_2031_03", "step_logs_2031_04", "step_logs_2031_05"), created)
            assertEquals(emptyList(), partitions.ensure(march, monthsAhead = 2))
            val bounds =
                jdbc.queryForObject(
                    "select pg_get_expr(relpartbound, oid) from pg_class where relname = 'step_logs_2031_05'",
                    String::class.java,
                )
            assertEquals("FOR VALUES FROM ('2031-05-01 00:00:00+00') TO ('2031-06-01 00:00:00+00')", bounds)
        } finally {
            for (month in listOf("03", "04", "05")) jdbc.execute("drop table if exists step_logs_2031_$month")
        }
    }

    @Test
    fun `the periodic check creates the partitions of the clock's month and the months ahead`() {
        clock.now = Instant.parse("2032-07-10T00:00:00Z")
        val months = listOf("2032_07", "2032_08", "2032_09")
        try {
            partitions.check()

            val sql = "select count(*) from pg_class where relname = any(?)"
            val names = months.map { "step_logs_$it" }.toTypedArray()
            val found = jdbc.queryForObject(sql, Int::class.java, names)
            assertEquals(3, found)
        } finally {
            for (month in months) jdbc.execute("drop table if exists step_logs_$month")
        }
    }

    @Test
    fun `a failed periodic check does not throw, so later checks still run`() {
        val broken = StepLogPartitions(JdbcTemplate(), clock, monthsAhead = 1, interval = Duration.ofDays(1))

        broken.check()
    }
}
