// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import java.util.UUID
import java.util.concurrent.Callable
import java.util.concurrent.Executors
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val RACERS = 10
private const val RACE_ROUNDS = 10
private val LATER: Duration = Duration.ofSeconds(7)

/** Cancel of a run (J1a): the guarded updates of the queue, the mark and the end of a cancelled run. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class, RunFinishedTestConfiguration::class)
class RunCancelsIntegrationTest(
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val cancels: RunCancels,
    @Autowired private val steps: StepTransitions,
    @Autowired private val told: RecordingStepsCancelled,
    @Autowired private val finished: RecordingRunFinished,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val sessions: TenantSessions,
) {
    private val tenant = RunsTenant(jdbc)

    @BeforeTest
    fun `a tenant with an agent`() {
        clock.now = RUNS_NOW
        told.calls.clear()
        finished.heard.clear()
        tenant.create()
    }

    @AfterTest
    fun `drop the tenant`() {
        tenant.drop()
    }

    private fun started(name: String = "prod-db"): RunView {
        val source = sources.create(tenant.id, tenant.draft(name))
        return runs.start(tenant.id, source.id)
    }

    private fun stepOf(run: RunView) = run.steps.single().id

    private fun running(run: RunView): UUID {
        val step = stepOf(run)
        assertTrue(steps.claim(tenant.id, step))
        assertTrue(steps.accepted(tenant.id, step, "accepted"))
        return step
    }

    @Test
    fun `a queued run is cancelled at once, its step never dispatched`() {
        val run = started()
        clock.now = RUNS_NOW + LATER

        val cancelled = cancels.cancel(tenant.id, run.id)

        assertEquals(RunState.CANCELLED, cancelled.status)
        assertEquals(RunOutcome.CANCELLED, cancelled.outcome)
        assertEquals(Cancellation(clock.now, "admin", CancelResult.CANCELLED), cancelled.cancellation)
        val step = cancelled.steps.single()
        assertEquals(StepState.CANCELLED, step.status)
        assertEquals("cancelled before dispatch", step.message)
        assertEquals(clock.now, step.finishedAt)
        assertNull(step.dispatchedAt)
        assertEquals(clock.now, cancelled.finishedAt)
    }

    @Test
    fun `a run cancelled in the queue is announced as finished, with no start`() {
        val run = started()
        clock.now = RUNS_NOW + LATER

        cancels.cancel(tenant.id, run.id)

        val event = finished.heard.single().event
        assertEquals(listOf(run.id, RunState.CANCELLED, null), listOf(event.runId, event.status, event.startedAt))
        assertEquals(clock.now, event.finishedAt)
    }

    @Test
    fun `a run in flight only gets the cancel mark and the dispatcher is told once`() {
        val run = started()
        running(run)
        clock.now = RUNS_NOW + LATER

        val cancelled = cancels.cancel(tenant.id, run.id)

        assertEquals(RunState.RUNNING, cancelled.status)
        assertEquals(RunOutcome.CANCELLING, cancelled.outcome)
        assertEquals(Cancellation(clock.now, "admin", CancelResult.PENDING), cancelled.cancellation)
        assertEquals(StepState.RUNNING, cancelled.steps.single().status)
        assertEquals(listOf(tenant.id to tenant.agentId), told.calls.toList())
    }

    @Test
    fun `a dispatched run is marked the same way`() {
        val run = started()
        assertTrue(steps.claim(tenant.id, stepOf(run)))

        val cancelled = cancels.cancel(tenant.id, run.id)

        assertEquals(RunOutcome.CANCELLING, cancelled.outcome)
        assertEquals(StepState.DISPATCHED, cancelled.steps.single().status)
    }

    @Test
    fun `a second cancel changes nothing and tells nobody`() {
        val run = started()
        running(run)
        val first = cancels.cancel(tenant.id, run.id)
        clock.now = RUNS_NOW + LATER

        val second = cancels.cancel(tenant.id, run.id)

        assertEquals(first.cancellation, second.cancellation)
        assertEquals(1, told.calls.size)
    }

    @Test
    fun `a cancelled run frees its source`() {
        val run = started()
        cancels.cancel(tenant.id, run.id)

        assertEquals(RunState.QUEUED, runs.start(tenant.id, run.sourceId).status)
    }

    @Test
    fun `a final run is not cancelled again`() {
        val run = started()
        tenant.finish(run.id)

        assertFailsWith<RunAlreadyFinished> { cancels.cancel(tenant.id, run.id) }

        assertEquals(RunState.SUCCEEDED, runs.get(tenant.id, run.id)?.status)
        assertEquals(emptyList(), told.calls.toList())
    }

    @Test
    fun `a cancelled run answers finished to another cancel`() {
        val run = started()
        cancels.cancel(tenant.id, run.id)

        assertFailsWith<RunAlreadyFinished> { cancels.cancel(tenant.id, run.id) }
    }

    @Test
    fun `a verification run cannot be cancelled until J1f`() {
        val run = started()
        jdbc.update("update runs set trigger = 'verification' where id = ?", run.id)

        assertFailsWith<RunNotCancellable> { cancels.cancel(tenant.id, run.id) }

        assertEquals(RunState.QUEUED, runs.get(tenant.id, run.id)?.status)
    }

    @Test
    fun `a run of another tenant is not found`() {
        val other = RunsTenant(jdbc).create()
        try {
            val source = sources.create(other.id, other.draft())
            val run = runs.start(other.id, source.id)

            assertFailsWith<RunNotFound> { cancels.cancel(tenant.id, run.id) }
            assertFailsWith<RunNotFound> { cancels.cancel(tenant.id, UUID.randomUUID()) }

            assertEquals(RunState.QUEUED, runs.get(other.id, run.id)?.status)
        } finally {
            other.drop()
        }
    }

    @Test
    fun `ten concurrent cancels leave one record of the cancel`() {
        val run = started()
        running(run)
        val pool = Executors.newFixedThreadPool(RACERS)
        try {
            val results =
                (1..RACERS).map { pool.submit(Callable { cancels.cancel(tenant.id, run.id) }) }.map { it.get() }
            assertEquals(1, results.map { it.cancellation }.toSet().size)
        } finally {
            pool.shutdownNow()
        }

        assertEquals(1, told.calls.size)
    }

    @Test
    fun `the agent's result after a cancel is the result of the run, the cancel says who finished first`() {
        val cases =
            listOf(
                Triple(StepState.CANCELLED, RunOutcome.CANCELLED, CancelResult.CANCELLED),
                Triple(StepState.SUCCEEDED, RunOutcome.SUCCEEDED, CancelResult.FINISHED_FIRST),
                Triple(StepState.FAILED, RunOutcome.FAILED, CancelResult.FINISHED_FIRST),
                Triple(StepState.TIMED_OUT, RunOutcome.TIMED_OUT, CancelResult.FINISHED_FIRST),
            )
        for ((agent, outcome, result) in cases) {
            val run = started("source-$agent")
            val step = running(run)
            cancels.cancel(tenant.id, run.id)

            assertTrue(steps.finished(tenant.id, step, StepOutcome(agent, null)), "$agent")

            val ended = runs.get(tenant.id, run.id)!!
            assertEquals(outcome, ended.outcome, "$agent")
            assertEquals(result, ended.cancellation?.result, "$agent")
        }
    }

    /** The agent went away and the lost deadline of the step has come (FXs). */
    private fun due(step: UUID) =
        jdbc.update("update run_steps set lost_deadline = ? where id = ?", java.sql.Timestamp.from(clock.now), step)

    @Test
    fun `a cancelled step the agent never confirmed is closed as cancelled, not lost`() {
        val run = started()
        val step = running(run)
        cancels.cancel(tenant.id, run.id)
        clock.now = RUNS_NOW + LATER
        due(step)

        assertTrue(steps.lost(tenant.id, step))

        val ended = runs.get(tenant.id, run.id)!!
        assertEquals(RunOutcome.CANCEL_UNCONFIRMED, ended.outcome)
        assertEquals(CancelResult.UNCONFIRMED, ended.cancellation?.result)
        val closed = ended.steps.single()
        assertEquals(StepState.CANCELLED, closed.status)
        assertEquals("the agent did not confirm the cancellation", closed.message)
        assertEquals("the agent did not confirm the cancellation", ended.message)
        assertEquals(clock.now, closed.finishedAt)
    }

    @Test
    fun `a step nobody cancelled is still lost at its deadline`() {
        val run = started()
        val step = running(run)
        due(step)

        assertTrue(steps.lost(tenant.id, step))

        assertEquals(RunOutcome.LOST, runs.get(tenant.id, run.id)!!.outcome)
    }

    @Test
    fun `a cancelled step the session refused is closed as cancelled instead of going back to the queue`() {
        val run = started()
        val step = stepOf(run)
        assertTrue(steps.claim(tenant.id, step))
        cancels.cancel(tenant.id, run.id)

        assertTrue(steps.release(tenant.id, step))

        val ended = runs.get(tenant.id, run.id)!!
        assertEquals(RunOutcome.CANCELLED, ended.outcome)
        assertEquals(CancelResult.CANCELLED, ended.cancellation?.result)
        assertEquals("cancelled before dispatch", ended.steps.single().message)
    }

    @Test
    fun `revoking the agent of a cancelled run loses its step with the reason of the revocation`() {
        val run = started()
        running(run)
        cancels.cancel(tenant.id, run.id)

        sessions.inTenant(tenant.id) { StepRevocation.loseSteps(it, tenant.id, tenant.agentId, clock.instant()) }

        val ended = runs.get(tenant.id, run.id)!!
        assertEquals(RunOutcome.LOST, ended.outcome)
        assertEquals("agent revoked", ended.steps.single().message)
        assertEquals(CancelResult.FINISHED_FIRST, ended.cancellation?.result)
    }

    @Test
    fun `the dispatcher's list of active steps says which ones were cancelled`() {
        val first = running(started("one"))
        val second = started("two")
        val cancelled = running(second)
        cancels.cancel(tenant.id, second.id)

        val active = steps.active(tenant.id, tenant.agentId).associate { it.id to it.cancelRequested }

        assertEquals(mapOf(first to false, cancelled to true), active)
    }

    private fun <T> together(vararg work: () -> T): List<Result<T>> {
        val pool = Executors.newFixedThreadPool(work.size)
        val go = java.util.concurrent.CountDownLatch(1)
        try {
            val futures =
                work.map { job ->
                    pool.submit(
                        Callable {
                            go.await()
                            runCatching { job() }
                        },
                    )
                }
            go.countDown()
            return futures.map { it.get() }
        } finally {
            pool.shutdownNow()
        }
    }

    @Test
    fun `a cancel racing the lost deadline ends the step one way, never both`() {
        repeat(RACE_ROUNDS) { round ->
            val run = started("race-$round")
            val step = running(run)
            due(step)

            val (cancelled, lost) =
                together(
                    { cancels.cancel(tenant.id, run.id) },
                    { steps.lost(tenant.id, step) },
                )

            val ended = runs.get(tenant.id, run.id)!!
            assertEquals(true, lost.getOrThrow(), "round $round: the deadline had come, one of the two closes the step")
            when (ended.steps.single().status) {
                StepState.CANCELLED -> assertEquals(CancelResult.UNCONFIRMED, ended.cancellation?.result)
                StepState.LOST -> assertTrue(cancelled.isFailure || ended.cancellation != null, "round $round")
                else -> error("round $round: ${ended.steps.single().status}")
            }
            assertTrue(ended.status == RunState.CANCELLED || ended.status == RunState.FAILED, "round $round")
        }
    }

    @Test
    fun `a cancel racing the agent's result leaves the agent's result as the run's`() {
        repeat(RACE_ROUNDS) { round ->
            val run = started("result-$round")
            val step = running(run)

            val (cancelled, _) =
                together(
                    { cancels.cancel(tenant.id, run.id) },
                    { steps.finished(tenant.id, step, StepOutcome(StepState.SUCCEEDED, null)) },
                )

            val ended = runs.get(tenant.id, run.id)!!
            assertEquals(RunState.SUCCEEDED, ended.status, "round $round")
            if (cancelled.isFailure) {
                assertTrue(cancelled.exceptionOrNull() is RunAlreadyFinished, "round $round")
            } else {
                assertEquals(CancelResult.FINISHED_FIRST, ended.cancellation?.result, "round $round")
            }
        }
    }
}
