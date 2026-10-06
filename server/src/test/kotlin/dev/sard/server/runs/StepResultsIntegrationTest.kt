// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.json.JsonMapper
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.concurrent.Callable
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val REPOSITORY_ID = "5f0c3e2d9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d"
private val LATER: Duration = Duration.ofSeconds(42)
private const val PARALLEL = 8
private val JSON = JsonMapper.builder().build()
private const val RACE_ROUNDS = 10
private val SUCCEEDED = StepReport(StepState.SUCCEEDED, null, StepOutput.Backup("4a3b2c1d", 1_000, 100, REPOSITORY_ID))

/** A published RunFinished and what the database held when the listener saw it. */
data class Heard(
    val event: RunFinished,
    val committedStatus: String,
)

/** Records every RunFinished with the run's status as another connection reads it. */
class RecordingRunFinished(
    private val jdbc: JdbcTemplate,
) : RunFinishedListener {
    val heard = CopyOnWriteArrayList<Heard>()

    override fun runFinished(event: RunFinished) {
        val status = jdbc.queryForObject("select status from runs where id = ?", String::class.java, event.runId)
        heard += Heard(event, status!!)
    }
}

@TestConfiguration(proxyBeanMethods = false)
class RunFinishedTestConfiguration {
    @Bean
    fun recordingRunFinished(jdbc: JdbcTemplate) = RecordingRunFinished(jdbc)
}

/**
 * A StepResult closes its step and run in one transaction, with the output and, for a backup,
 * the snapshot (S7a tests 1–3, 6, 7); RunFinished follows the commit, once.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class, RunFinishedTestConfiguration::class)
class StepResultsIntegrationTest(
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val steps: StepTransitions,
    @Autowired private val results: StepResults,
    @Autowired private val finished: RecordingRunFinished,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc)
    private val other = RunsTenant(jdbc)

    @BeforeTest
    fun `two tenants with an agent each`() {
        clock.now = RUNS_NOW
        finished.heard.clear()
        tenant.create()
        other.create()
    }

    @AfterTest
    fun `drop both tenants`() {
        jdbc.update("drop trigger if exists snapshots_fail on snapshots")
        for (t in listOf(tenant, other)) jdbc.update("delete from snapshots where tenant_id = ?", t.id)
        tenant.drop()
        other.drop()
    }

    private fun started(name: String = "prod-db"): RunView {
        val source = sources.create(tenant.id, tenant.draft(name))
        return runs.start(tenant.id, source.id)
    }

    /** A run whose single step the agent accepted. */
    private fun running(): Pair<RunView, UUID> {
        val run = started()
        val step = run.steps.single().id
        assertTrue(steps.claim(tenant.id, step))
        assertTrue(steps.accepted(tenant.id, step, "accepted"))
        clock.now = RUNS_NOW + LATER
        return run to step
    }

    private fun record(
        step: UUID,
        report: StepReport = SUCCEEDED,
        agent: UUID = tenant.agentId,
        tenantId: UUID = tenant.id,
    ) = results.record(tenantId, agent, step, report)

    private fun row(
        table: String,
        id: UUID,
    ): Map<String, Any?> = jdbc.queryForMap("select * from $table where id = ?", id)

    private fun snapshots(step: UUID) = jdbc.queryForList("select * from snapshots where step_id = ?", step)

    private fun instant(value: Any?): Instant? = (value as Timestamp?)?.toInstant()

    private fun json(value: Any?) = value?.let { JSON.readTree(it.toString()) }

    /** The step's lost deadline has come (FXs: set by StepDeadlines when its agent went away). */
    private fun due(step: UUID) {
        jdbc.update("update run_steps set lost_deadline = ? where id = ?", Timestamp.from(clock.now), step)
    }

    @Test
    fun `a succeeded backup closes the step and the run and records its snapshot`() {
        val (run, step) = running()

        assertEquals(Recorded.Closed(StepState.SUCCEEDED, invalid = null), record(step))

        val stepRow = row("run_steps", step)
        val output = json(SUCCEEDED.output!!.json())
        assertEquals(
            listOf<Any?>("succeeded", null, clock.now, output),
            listOf(stepRow["status"], stepRow["message"], instant(stepRow["finished_at"]), json(stepRow["output"])),
        )
        val runRow = row("runs", run.id)
        assertEquals(listOf<Any?>("succeeded", clock.now), listOf(runRow["status"], instant(runRow["finished_at"])))
        val snapshot = snapshots(step).single()
        assertEquals(
            listOf<Any?>(tenant.id, run.sourceId, tenant.agentId, REPOSITORY, REPOSITORY_ID, "4a3b2c1d", 1_000L, 100L),
            listOf("tenant_id", "source_id", "agent_id", "repository_name", "repository_id", "snapshot_id")
                .map { snapshot[it] } + listOf(snapshot["total_bytes"], snapshot["added_bytes"]),
        )
        assertEquals(clock.now, instant(snapshot["created_at"]))
        assertNull(snapshot["forgotten_at"])
    }

    @Test
    fun `RunFinished follows the commit once, with the run's trigger, source, agent and outcome`() {
        val (run, step) = running()

        record(step)
        record(step)

        val expected =
            RunFinished(
                tenant.id,
                run.id,
                run.sourceId,
                tenant.agentId,
                Trigger.MANUAL,
                RunState.SUCCEEDED,
                null,
                clock.now,
            )
        assertEquals(listOf(Heard(expected, "succeeded")), finished.heard)
    }

    @Test
    fun `a rejected step fails its run with the agent's message and records no snapshot`() {
        val run = started()
        val step = run.steps.single().id
        steps.claim(tenant.id, step)

        val report = StepReport(StepState.REJECTED, "unknown plugin \"absent\"", null)
        assertEquals(Recorded.Closed(StepState.REJECTED, invalid = null), record(step, report))

        val stepRow = row("run_steps", step)
        val stepFields = listOf(stepRow["status"], stepRow["message"], stepRow["output"])
        assertEquals(listOf<Any?>("rejected", "unknown plugin \"absent\"", null), stepFields)
        val runRow = row("runs", run.id)
        assertEquals(listOf<Any?>("failed", "unknown plugin \"absent\""), listOf(runRow["status"], runRow["message"]))
        assertEquals(emptyList(), snapshots(step))
        assertEquals(listOf(RunState.FAILED), finished.heard.map { it.event.status })
    }

    @Test
    fun `a succeeded backup without its snapshot fails as an invalid result`() {
        val (run, step) = running()

        val recorded = record(step, StepReport(StepState.SUCCEEDED, null, null))

        assertEquals(Recorded.Closed(StepState.FAILED, "a succeeded backup without its output"), recorded)
        val message = "invalid result: a succeeded backup without its output"
        val stepRow = row("run_steps", step)
        assertEquals(listOf<Any?>("failed", message), listOf(stepRow["status"], stepRow["message"]))
        assertEquals("failed", row("runs", run.id)["status"])
        assertEquals(emptyList(), snapshots(step))
    }

    @Test
    fun `a repeated result changes nothing and is reported as repeated`() {
        val (run, step) = running()
        record(step)
        val stepBefore = row("run_steps", step)
        val runBefore = row("runs", run.id)
        clock.now = RUNS_NOW + LATER + LATER

        val failed = StepReport(StepState.FAILED, "disk full", null)
        assertEquals(Recorded.Repeated, record(step, failed))
        assertEquals(Recorded.Repeated, record(step))

        assertEquals(stepBefore, row("run_steps", step))
        assertEquals(runBefore, row("runs", run.id))
        assertEquals(1, snapshots(step).size)
        assertEquals(1, finished.heard.size)
    }

    @Test
    fun `parallel copies of one result close the step once`() {
        val (_, step) = running()
        val pool = Executors.newFixedThreadPool(PARALLEL)
        val start = CountDownLatch(1)
        try {
            val calls =
                List(PARALLEL) {
                    pool.submit(
                        Callable {
                            start.await()
                            record(step)
                        },
                    )
                }
            start.countDown()
            val recorded = calls.map { it.get(RUNS_RACE_WAIT.toSeconds(), TimeUnit.SECONDS) }

            assertEquals(1, recorded.count { it is Recorded.Closed })
            assertEquals(PARALLEL - 1, recorded.count { it == Recorded.Repeated })
        } finally {
            pool.shutdownNow()
        }
        assertEquals(1, snapshots(step).size)
        assertEquals(1, finished.heard.size)
    }

    @Test
    fun `a result for a step of another agent or tenant is unknown and changes nothing`() {
        val (run, step) = running()
        val sibling = UUID.randomUUID()
        tenant.insertAgent(sibling)
        val before = row("run_steps", step)

        assertEquals(Recorded.Unknown, record(step, agent = sibling))
        assertEquals(Recorded.Unknown, record(step, agent = other.agentId, tenantId = other.id))
        assertEquals(Recorded.Unknown, record(UUID.randomUUID()))

        assertEquals(before, row("run_steps", step))
        assertEquals("running", row("runs", run.id)["status"])
        assertEquals(emptyList(), snapshots(step))
        assertEquals(emptyList(), finished.heard)
    }

    @Test
    fun `a result for a step not yet dispatched is not recorded`() {
        val run = started()
        val step = run.steps.single().id

        assertEquals(Recorded.NotDispatched, record(step))

        assertEquals("queued", row("run_steps", step)["status"])
        assertEquals(emptyList(), snapshots(step))
    }

    @Test
    fun `a database failure leaves nothing behind, and the agent's next copy is recorded`() {
        val (run, step) = running()
        jdbc.execute(
            """
            create or replace function snapshots_fail() returns trigger language plpgsql as
            ${'$'}${'$'} begin raise exception 'disk full'; end ${'$'}${'$'};
            create trigger snapshots_fail before insert on snapshots for each row execute function snapshots_fail();
            """.trimIndent(),
        )

        assertFailsWith<RuntimeException> { record(step) }
        val statuses = listOf(row("run_steps", step)["status"], row("runs", run.id)["status"])
        assertEquals(listOf<Any?>("running", "running"), statuses)
        assertEquals(emptyList(), finished.heard)

        jdbc.update("drop trigger snapshots_fail on snapshots")
        assertEquals(Recorded.Closed(StepState.SUCCEEDED, invalid = null), record(step))
        assertEquals("succeeded", row("runs", run.id)["status"])
        assertEquals(1, finished.heard.size)
    }

    @Test
    fun `the lost window publishes RunFinished for the failed run`() {
        val (run, step) = running()
        due(step)

        assertTrue(steps.lost(tenant.id, step))

        val event = finished.heard.single()
        assertEquals(Heard(event.event.copy(runId = run.id, status = RunState.FAILED), "failed"), event)
        assertEquals(LOST_MESSAGE, event.event.message)
    }

    @Test
    fun `a late result keeps the step lost and records its output and snapshot once`() {
        val (run, step) = running()
        due(step)
        steps.lost(tenant.id, step)
        val runBefore = row("runs", run.id)

        assertEquals(Recorded.Late(kept = true), record(step))
        assertEquals(Recorded.Late(kept = false), record(step))

        val stepRow = row("run_steps", step)
        assertEquals(listOf<Any?>("lost", LOST_MESSAGE), listOf(stepRow["status"], stepRow["message"]))
        assertEquals(json(SUCCEEDED.output!!.json()), json(stepRow["output"]))
        assertEquals(runBefore, row("runs", run.id))
        assertEquals(1, snapshots(step).size)
        assertEquals(1, finished.heard.size)
    }

    @Test
    fun `a late invalid or failed result keeps nothing`() {
        val (_, step) = running()
        due(step)
        steps.lost(tenant.id, step)

        assertEquals(Recorded.Late(kept = false), record(step, StepReport(StepState.SUCCEEDED, null, null)))
        assertEquals(Recorded.Late(kept = false), record(step, StepReport(StepState.FAILED, "disk full", null)))

        assertNull(row("run_steps", step)["output"])
        assertEquals(emptyList(), snapshots(step))
    }

    @Test
    fun `a result racing the lost window ends one way, and the run finishes once`() {
        repeat(RACE_ROUNDS) { round ->
            finished.heard.clear()
            val source = sources.create(tenant.id, tenant.draft("race-$round"))
            val run = runs.start(tenant.id, source.id)
            val step = run.steps.single().id
            steps.claim(tenant.id, step)
            steps.accepted(tenant.id, step, "accepted")
            due(step)
            val pool = Executors.newFixedThreadPool(2)
            val start = CountDownLatch(1)
            try {
                val lost =
                    pool.submit(
                        Callable {
                            start.await()
                            steps.lost(tenant.id, step)
                        },
                    )
                val backup = StepOutput.Backup("race-$round", 1_000, 100, REPOSITORY_ID)
                val report = StepReport(StepState.SUCCEEDED, null, backup)
                val result =
                    pool.submit(
                        Callable {
                            start.await()
                            record(step, report)
                        },
                    )
                start.countDown()
                val lostWon = lost.get(RUNS_RACE_WAIT.toSeconds(), TimeUnit.SECONDS)
                val recorded = result.get(RUNS_RACE_WAIT.toSeconds(), TimeUnit.SECONDS)

                val expected = if (lostWon) Recorded.Late(kept = true) else Recorded.Closed(StepState.SUCCEEDED, null)
                assertEquals(expected, recorded)
                val status = if (lostWon) "lost" else "succeeded"
                assertEquals(status, row("run_steps", step)["status"])
                assertEquals(1, snapshots(step).size)
                assertEquals(1, finished.heard.size)
            } finally {
                pool.shutdownNow()
            }
        }
    }
}
