// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.concurrent.Callable
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val RACE_ROUNDS = 10
private val LATER: Duration = Duration.ofSeconds(7)

/**
 * Guarded step transitions (ADR 0038): each one names the status it expects, so dispatch,
 * reconciliation and S7 never overwrite each other; the run follows its step in the same transaction.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class StepTransitionsIntegrationTest(
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val steps: StepTransitions,
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
        tenant.drop()
        other.drop()
    }

    private fun started(name: String = "prod-db"): RunView {
        val source = sources.create(tenant.id, tenant.draft(name))
        return runs.start(tenant.id, source.id)
    }

    private fun stepOf(run: RunView) = run.steps.single().id

    private fun row(
        table: String,
        id: UUID,
    ): Map<String, Any?> = jdbc.queryForMap("select * from $table where id = ?", id)

    /** The step's lost deadline has come (FXs: set by StepDeadlines when its agent went away). */
    private fun due(step: UUID) =
        jdbc.update("update run_steps set lost_deadline = ? where id = ?", java.sql.Timestamp.from(clock.now), step)

    private fun instant(value: Any?): Instant? = (value as Timestamp?)?.toInstant()

    private fun running(run: RunView): UUID {
        val step = stepOf(run)
        assertTrue(steps.claim(tenant.id, step))
        assertTrue(steps.accepted(tenant.id, step, "accepted"))
        return step
    }

    @Test
    fun `claim takes a queued step once and dispatches its run`() {
        val run = started()
        clock.now = RUNS_NOW + LATER

        assertTrue(steps.claim(tenant.id, stepOf(run)))
        assertFalse(steps.claim(tenant.id, stepOf(run)))

        val step = row("run_steps", stepOf(run))
        assertEquals(listOf<Any?>("dispatched", clock.now), listOf(step["status"], instant(step["dispatched_at"])))
        assertEquals("dispatched", row("runs", run.id)["status"])
    }

    @Test
    fun `release puts a dispatched step back in the queue, and nothing else`() {
        val run = started()
        assertFalse(steps.release(tenant.id, stepOf(run)))
        steps.claim(tenant.id, stepOf(run))

        assertTrue(steps.release(tenant.id, stepOf(run)))

        val step = row("run_steps", stepOf(run))
        assertEquals(listOf<Any?>("queued", null), listOf(step["status"], step["dispatched_at"]))
        assertEquals("queued", row("runs", run.id)["status"])
    }

    @Test
    fun `redispatch takes only a step dispatched before the given instant`() {
        val run = started()
        steps.claim(tenant.id, stepOf(run))
        clock.now = RUNS_NOW + LATER

        assertFalse(steps.redispatch(tenant.id, stepOf(run), RUNS_NOW))
        assertTrue(steps.redispatch(tenant.id, stepOf(run), RUNS_NOW + Duration.ofMillis(1)))

        assertEquals(clock.now, instant(row("run_steps", stepOf(run))["dispatched_at"]))
        assertFalse(steps.redispatch(tenant.id, stepOf(run), RUNS_NOW + Duration.ofMillis(1)), "sent again at now")
    }

    @Test
    fun `accepted starts a dispatched step and its run`() {
        val run = started()
        assertFalse(steps.accepted(tenant.id, stepOf(run), "accepted"), "a queued step was never sent")
        steps.claim(tenant.id, stepOf(run))
        clock.now = RUNS_NOW + LATER

        assertTrue(steps.accepted(tenant.id, stepOf(run), "accepted"))

        val step = row("run_steps", stepOf(run))
        val stored = listOf(step["status"], step["phase"], instant(step["started_at"]))
        assertEquals(listOf<Any?>("running", "accepted", clock.now), stored)
        val runRow = row("runs", run.id)
        assertEquals(listOf<Any?>("running", clock.now), listOf(runRow["status"], instant(runRow["started_at"])))
    }

    @Test
    fun `a finished step finishes its run and frees the source (D6)`() {
        val run = started()
        val step = running(run)
        clock.now = RUNS_NOW + LATER

        assertTrue(steps.finished(tenant.id, step, StepOutcome(StepState.SUCCEEDED, null)))

        val stepRow = row("run_steps", step)
        assertEquals(listOf<Any?>("succeeded", clock.now), listOf(stepRow["status"], instant(stepRow["finished_at"])))
        val runRow = row("runs", run.id)
        assertEquals(
            listOf<Any?>("succeeded", null, clock.now),
            listOf(runRow["status"], runRow["message"], instant(runRow["finished_at"])),
        )
        runs.start(tenant.id, run.sourceId)
    }

    @Test
    fun `a step the agent rejected before accepting it fails the run with the agent's message`() {
        val run = started()
        steps.claim(tenant.id, stepOf(run))

        assertTrue(steps.finished(tenant.id, stepOf(run), StepOutcome(StepState.REJECTED, "unknown plugin \"x\"")))

        val stepRow = row("run_steps", stepOf(run))
        assertEquals(listOf<Any?>("rejected", "unknown plugin \"x\""), listOf(stepRow["status"], stepRow["message"]))
        assertNull(stepRow["started_at"])
        val runRow = row("runs", run.id)
        assertEquals(listOf<Any?>("failed", "unknown plugin \"x\""), listOf(runRow["status"], runRow["message"]))
    }

    @Test
    fun `a closed step does not finish twice, and a queued one was never sent`() {
        val run = started()
        assertFalse(steps.finished(tenant.id, stepOf(run), StepOutcome(StepState.FAILED, "boom")))
        val step = running(run)
        steps.finished(tenant.id, step, StepOutcome(StepState.SUCCEEDED, null))

        assertFalse(steps.finished(tenant.id, step, StepOutcome(StepState.FAILED, "again")))
        assertEquals("succeeded", row("run_steps", step)["status"])
    }

    @Test
    fun `an outcome is a final state`() {
        assertFailsWith<IllegalArgumentException> { StepOutcome(StepState.RUNNING, null) }
    }

    @Test
    fun `a running step becomes lost once its deadline has come and fails its run, once`() {
        val run = started()
        val step = running(run)
        assertFalse(steps.lost(tenant.id, step), "no deadline: the agent has not gone away")
        jdbc.update(
            "update run_steps set lost_deadline = ? where id = ?",
            java.sql.Timestamp.from(RUNS_NOW + LATER),
            step,
        )
        assertFalse(steps.lost(tenant.id, step), "the deadline has not come")
        clock.now = RUNS_NOW + LATER

        assertTrue(steps.lost(tenant.id, step))
        assertFalse(steps.lost(tenant.id, step))

        val stepRow = row("run_steps", step)
        assertEquals(
            listOf<Any?>("lost", LOST_MESSAGE, clock.now),
            listOf(stepRow["status"], stepRow["message"], instant(stepRow["finished_at"])),
        )
        val runRow = row("runs", run.id)
        assertEquals(listOf<Any?>("failed", LOST_MESSAGE), listOf(runRow["status"], runRow["message"]))
    }

    @Test
    fun `a dispatched step whose deadline has come is lost too, a queued one never`() {
        val queued = stepOf(started("queued"))
        due(queued)
        assertFalse(steps.lost(tenant.id, queued))

        val run = started()
        steps.claim(tenant.id, stepOf(run))
        due(stepOf(run))

        assertTrue(steps.lost(tenant.id, stepOf(run)))
        val stored = listOf(row("run_steps", stepOf(run))["status"], row("runs", run.id)["status"])
        assertEquals(listOf<Any?>("lost", "failed"), stored)
    }

    @Test
    fun `a send, progress or a result clears the deadline`() {
        val step = stepOf(started())
        steps.claim(tenant.id, step)
        due(step)
        steps.release(tenant.id, step)
        assertNull(row("run_steps", step)["lost_deadline"], "release")
        steps.claim(tenant.id, step)
        due(step)
        assertTrue(steps.redispatch(tenant.id, step, clock.now + LATER))
        assertNull(row("run_steps", step)["lost_deadline"], "redispatch")
        due(step)
        steps.accepted(tenant.id, step, "accepted")
        assertNull(row("run_steps", step)["lost_deadline"], "accepted")
        due(step)
        steps.finished(tenant.id, step, StepOutcome(StepState.SUCCEEDED, null))
        assertNull(row("run_steps", step)["lost_deadline"], "result")
    }

    @Test
    fun `a result that arrived first keeps its state, the window finds nothing to mark`() {
        val step = running(started())
        due(step)
        assertTrue(steps.finished(tenant.id, step, StepOutcome(StepState.SUCCEEDED, null)))
        assertFalse(steps.lost(tenant.id, step))
        assertEquals("succeeded", row("run_steps", step)["status"])
    }

    @Test
    fun `a step marked lost first stays lost, a late result changes nothing`() {
        val step = running(started())
        due(step)
        assertTrue(steps.lost(tenant.id, step))
        assertFalse(steps.finished(tenant.id, step, StepOutcome(StepState.SUCCEEDED, null)))
        assertEquals("lost", row("run_steps", step)["status"])
    }

    @Test
    fun `lost and a result racing on one step, exactly one wins and its state stays`() {
        val pool = Executors.newFixedThreadPool(2)
        try {
            repeat(RACE_ROUNDS) { round ->
                val run = started("race-$round")
                val step = running(run)
                due(step)
                val go = CountDownLatch(1)
                val lost =
                    pool.submit(
                        Callable {
                            go.await()
                            steps.lost(tenant.id, step)
                        },
                    )
                val result =
                    pool.submit(
                        Callable {
                            go.await()
                            steps.finished(tenant.id, step, StepOutcome(StepState.SUCCEEDED, null))
                        },
                    )
                go.countDown()
                val wins = listOf(lost, result).map { it.get(RUNS_RACE_WAIT.toSeconds(), TimeUnit.SECONDS) }

                assertEquals(1, wins.count { it }, "round $round: $wins")
                val expected = if (wins[0]) listOf("lost", "failed") else listOf("succeeded", "succeeded")
                val stored = listOf(row("run_steps", step)["status"], row("runs", run.id)["status"])
                assertEquals(expected, stored, "round $round")
            }
        } finally {
            pool.shutdownNow()
        }
    }

    @Test
    fun `another tenant's steps can neither be read nor moved`() {
        val run = started()
        val step = stepOf(run)

        assertEquals(emptyList(), steps.active(other.id, tenant.agentId))
        assertFalse(steps.claim(other.id, step))
        steps.claim(tenant.id, step)
        assertFalse(steps.release(other.id, step))
        assertFalse(steps.redispatch(other.id, step, RUNS_NOW + LATER))
        assertFalse(steps.accepted(other.id, step, "accepted"))
        steps.accepted(tenant.id, step, "accepted")
        assertFalse(steps.lost(other.id, step))
        assertFalse(steps.finished(other.id, step, StepOutcome(StepState.FAILED, "x")))
        assertEquals("running", row("run_steps", step)["status"])
    }

    @Test
    fun `an agent's active steps come in creation order and leave the list once final`() {
        val first = started("a")
        clock.now = RUNS_NOW + Duration.ofSeconds(1)
        val second = started("b")
        clock.now = RUNS_NOW + Duration.ofSeconds(2)
        val third = started("c")
        val done = running(third)
        steps.finished(tenant.id, done, StepOutcome(StepState.SUCCEEDED, null))

        val active = steps.active(tenant.id, tenant.agentId)

        assertEquals(listOf(stepOf(first), stepOf(second)), active.map { it.id })
        assertEquals(listOf(StepState.QUEUED, StepState.QUEUED), active.map { it.status })
        assertEquals(CONFIG, active.first().config)
    }
}
