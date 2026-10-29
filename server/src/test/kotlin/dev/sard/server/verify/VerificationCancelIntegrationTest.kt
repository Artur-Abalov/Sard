// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.CANCELLED_BEFORE_DISPATCH
import dev.sard.server.runs.CANCEL_UNCONFIRMED_MESSAGE
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RecordingStepsCancelled
import dev.sard.server.runs.RecordingStepsQueued
import dev.sard.server.runs.RunAlreadyFinished
import dev.sard.server.runs.RunCancels
import dev.sard.server.runs.RunNotFound
import dev.sard.server.runs.RunOutcome
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.SourceFingerprint
import dev.sard.server.runs.Sources
import dev.sard.server.runs.StageState
import dev.sard.server.runs.StepOutput
import dev.sard.server.runs.StepReport
import dev.sard.server.runs.StepResults
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepTransitions
import dev.sard.server.runs.VerifierVerdict
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Duration
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val REPOSITORY_ID = "c".repeat(64)
private val SOURCE_MACHINE = "1".repeat(64)
private val VERIFIER_MACHINE = "2".repeat(64)
private val LATER: Duration = Duration.ofSeconds(5)

/**
 * J1f against PostgreSQL and the real run machinery: an administrator cancels a restore verification job. A job in
 * the queue closes at once; a job in flight is marked and its verifier is told; the end of the attempt closes the
 * job with a record `cancelled`, never with a retry and never as the third strike; a verdict that came first wins.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = [
        "spring.grpc.server.port=0",
        "sard.verification.interval=1h",
        "sard.verification.default-timezone=UTC",
        "sard.scheduler.interval=1h",
        "sard.freshness.interval=1h",
    ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class, VerificationTestConfiguration::class)
class VerificationCancelIntegrationTest(
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val steps: StepTransitions,
    @Autowired private val results: StepResults,
    @Autowired private val loop: VerificationLoop,
    @Autowired private val online: TestOnlineAgents,
    @Autowired private val queued: RecordingStepsQueued,
    @Autowired private val told: RecordingStepsCancelled,
    @Autowired private val clock: MovableClock,
    @Autowired private val verifications: Verifications,
    @Autowired private val cancels: VerificationCancels,
    @Autowired private val runCancels: RunCancels,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val history: VerificationHistory,
) {
    private lateinit var tenant: RunsTenant
    private lateinit var sourceId: UUID

    @BeforeTest
    fun setUp() {
        clock.now = RUNS_NOW
        online.online.clear()
        queued.calls.clear()
        told.calls.clear()
        tenant = RunsTenant(jdbc).create()
        jdbc.update("update agents set machine_id = ? where id = ?", SOURCE_MACHINE, tenant.agentId)
        jdbc.update("update agent_repositories set repository_id = ? where agent_id = ?", REPOSITORY_ID, tenant.agentId)
        sourceId = sources.create(tenant.id, tenant.draft()).id
        backup(sourceId)
        queued.calls.clear()
    }

    @AfterTest
    fun tearDown() {
        val tables =
            listOf(
                "no_verifier_episodes",
                "verification_stale_episodes",
                "restore_verifications",
                "verification_jobs",
                "snapshots",
            )
        val admin =
            dev.sard.server.TestAdmin
                .of(jdbc)
        for (table in tables) {
            admin.update("delete from $table where tenant_id = ?", tenant.id)
        }
        jdbc.update("delete from verification_schedule")
        jdbc.update("delete from step_logs where tenant_id = ?", tenant.id)
        tenant.drop()
    }

    // --- fixtures

    private fun backup(
        source: UUID,
        fingerprint: SourceFingerprint? = null,
    ): String {
        val run = runs.start(tenant.id, source)
        val step = run.steps.single().id
        steps.claim(tenant.id, step)
        val snapshot = "snap-${UUID.randomUUID()}"
        val output =
            StepOutput.Backup(snapshot, 2_000, 200, REPOSITORY_ID, fingerprint = fingerprint, totalFiles = null)
        results.record(tenant.id, tenant.agentId, step, StepReport(StepState.SUCCEEDED, null, output))
        return snapshot
    }

    private fun verifier(): UUID {
        val id = UUID.randomUUID()
        jdbc.update(
            "insert into agents (id, tenant_id, hostname, registered_at, role, machine_id, verifier_reported) " +
                "values (?, ?, ?, ?, 'verifier', ?, true)",
            id,
            tenant.id,
            "verifier-$id",
            Timestamp.from(RUNS_NOW),
            VERIFIER_MACHINE,
        )
        jdbc.update(
            "insert into agent_plugins (tenant_id, agent_id, name, version, config_schema, actions) " +
                "values (?, ?, 'postgresql', '0.1.0', '{}'::jsonb, array['verify'])",
            tenant.id,
            id,
        )
        jdbc.update(
            "insert into agent_repositories (tenant_id, agent_id, name, backend, repository_id) " +
                "values (?, ?, 'shared', 's3', ?)",
            tenant.id,
            id,
            REPOSITORY_ID,
        )
        online.online += id
        return id
    }

    private fun jobs(): List<Map<String, Any?>> =
        jdbc.queryForList(
            "select id, status, failed_attempts, run_id, cancel_requested_at, cancel_requested_by, cancel_result " +
                "from verification_jobs where tenant_id = ? order by created_at, id",
            tenant.id,
        )

    private fun records(): List<Map<String, Any?>> =
        jdbc.queryForList(
            "select status, level, reason, step_id, verifier_agent_id, recovered, cancelled_by, cancelled_at " +
                "from restore_verifications where tenant_id = ? order by verified_at, id",
            tenant.id,
        )

    private fun stepsOf(agent: UUID): Int? {
        val sql = "select count(*) from run_steps where agent_id = ?"
        return jdbc.queryForObject(sql, Int::class.java, agent)
    }

    /** How many attempts (runs of a verification job) were ever made. */
    private fun attempts(): Int? {
        val sql = "select count(*) from runs where trigger = 'verification'"
        return jdbc.queryForObject(sql, Int::class.java)
    }

    private fun jobId() = jobs().first()["id"] as UUID

    private fun attempt(): Map<String, Any?> =
        jdbc
            .queryForList(
                "select st.id, st.agent_id, st.status as step_status, st.message, r.id as run_id, " +
                    "r.status as run_status, r.cancel_result " +
                    "from verification_jobs j join runs r on r.id = j.run_id join run_steps st on st.run_id = r.id " +
                    "where j.tenant_id = ? order by j.created_at desc, j.id limit 1",
                tenant.id,
            ).single()

    private fun stepId() = attempt()["id"] as UUID

    /** A job handed to a verifier whose step is queued: the verifier is offline. */
    private fun jobWithQueuedAttempt(): UUID {
        val verifier = verifier()
        loop.tick()
        online.online -= verifier
        queued.calls.clear()
        return verifier
    }

    /** A job whose attempt runs at the verifier. */
    private fun jobWithRunningAttempt(): UUID {
        val verifier = verifier()
        loop.tick()
        steps.claim(tenant.id, stepId())
        steps.accepted(tenant.id, stepId(), "accepted")
        queued.calls.clear()
        return verifier
    }

    private fun cancelledReport(
        verifier: UUID,
        message: String = "cancelled by the server",
    ) = results.record(tenant.id, verifier, stepId(), StepReport(StepState.CANCELLED, message, null))

    private fun stage(
        level: Int,
        state: StageState = StageState.PASSED,
    ) = StepOutput.Stage(level, "stage-$level", state, null, null, 0, "", "")

    private fun verdict(
        verdict: VerifierVerdict,
        failing: Boolean = false,
    ) = StepOutput.Verify(
        "snap",
        emptyList(),
        if (failing) listOf(stage(1), stage(2, StageState.FAILED)) else (0..4).map(::stage),
        verdict,
        emptyList(),
        90_000,
    )

    // --- a job in the queue

    @Test
    fun `a job in the queue without an attempt is closed at once and no verifier ever gets its step`() {
        val verifier = verifier()
        online.online.clear()
        loop.tick()
        val job = jobId()
        assertNull(jobs().single()["run_id"])
        clock.now = RUNS_NOW + LATER

        val source = cancels.cancel(tenant.id, job)

        assertEquals(sourceId, source)
        assertEquals("closed", jobs().single()["status"])
        val record = records().single()
        assertEquals("cancelled", record["status"])
        assertEquals(0, record["level"])
        assertNull(record["step_id"])
        assertEquals("admin", record["cancelled_by"])
        assertEquals(Timestamp.from(clock.now), record["cancelled_at"])
        assertEquals("cancelled", jobs().single()["cancel_result"])
        online.online += verifier
        loop.tick()
        assertEquals(0, stepsOf(verifier))
        assertEquals(emptyList(), queued.calls)
    }

    // --- a job with an attempt

    @Test
    fun `a job whose attempt is queued is closed with the attempt, and the verifier is told nothing`() {
        val verifier = jobWithQueuedAttempt()
        val job = jobId()

        cancels.cancel(tenant.id, job)

        val attempt = attempt()
        assertEquals("cancelled", attempt["run_status"])
        assertEquals("cancelled", attempt["step_status"])
        assertEquals(CANCELLED_BEFORE_DISPATCH, attempt["message"])
        assertEquals("closed", jobs().single()["status"])
        val record = records().single()
        assertEquals("cancelled", record["status"])
        assertEquals(CANCELLED_BEFORE_DISPATCH, record["reason"])
        assertEquals(attempt["id"], record["step_id"])
        assertEquals(verifier, record["verifier_agent_id"])
        assertEquals(emptyList(), told.calls)
        online.online += verifier
        loop.tick()
        assertEquals(1, stepsOf(verifier))
    }

    @Test
    fun `a job whose attempt runs is marked, the verifier is sent a cancel and the job stays open`() {
        val verifier = jobWithRunningAttempt()
        clock.now = RUNS_NOW + LATER

        cancels.cancel(tenant.id, jobId())

        val job = jobs().single()
        assertEquals("running", job["status"])
        assertEquals("pending", job["cancel_result"])
        assertEquals("admin", job["cancel_requested_by"])
        assertEquals(Timestamp.from(clock.now), job["cancel_requested_at"])
        assertEquals(listOf(tenant.id to verifier), told.calls)
        assertEquals(emptyList(), records())
        assertEquals("running", attempt()["run_status"])
    }

    @Test
    fun `cancelling the attempt run cancels its job the same way`() {
        val verifier = jobWithRunningAttempt()
        val run = attempt()["run_id"] as UUID

        val view = runCancels.cancel(tenant.id, run)

        assertEquals(RunOutcome.CANCELLING, view.outcome)
        assertEquals("pending", jobs().single()["cancel_result"])
        assertEquals(listOf(tenant.id to verifier), told.calls)
    }

    @Test
    fun `a second cancel keeps the first mark and tells the verifier nothing more`() {
        jobWithRunningAttempt()
        clock.now = RUNS_NOW + LATER
        cancels.cancel(tenant.id, jobId())
        told.calls.clear()
        clock.now = RUNS_NOW + LATER + LATER

        cancels.cancel(tenant.id, jobId())

        assertEquals(Timestamp.from(RUNS_NOW + LATER), jobs().single()["cancel_requested_at"])
        assertEquals("pending", jobs().single()["cancel_result"])
        assertEquals(emptyList(), told.calls)
    }

    // --- the end of a cancelled attempt

    @Test
    fun `the verifier confirming the cancel closes the job with a record cancelled and no new attempt`() {
        val verifier = jobWithRunningAttempt()
        jdbc.update("update verification_jobs set failed_attempts = 1")
        clock.now = RUNS_NOW + LATER
        cancels.cancel(tenant.id, jobId())
        clock.now = RUNS_NOW + LATER + LATER

        cancelledReport(verifier)
        loop.tick()

        val job = jobs().single()
        assertEquals("closed", job["status"])
        assertEquals("cancelled", job["cancel_result"])
        val record = records().single()
        assertEquals("cancelled", record["status"])
        assertEquals(0, record["level"])
        assertEquals("cancelled by the server", record["reason"])
        assertEquals("admin", record["cancelled_by"])
        assertEquals(Timestamp.from(RUNS_NOW + LATER), record["cancelled_at"])
        assertEquals(false, record["recovered"])
        assertEquals(1, attempts())
        assertEquals(listOf(tenant.id to verifier), told.calls)
    }

    @Test
    fun `a cancel after two attempts without a verdict records cancelled, not an infrastructure failure`() {
        val verifier = jobWithRunningAttempt()
        jdbc.update("update verification_jobs set failed_attempts = 2")
        cancels.cancel(tenant.id, jobId())

        cancelledReport(verifier)
        loop.tick()

        assertEquals(listOf("cancelled"), records().map { it["status"] })
    }

    private fun verdictBeforeCancel(
        verdict: VerifierVerdict,
        step: StepState,
        status: String,
    ) {
        val verifier = jobWithRunningAttempt()
        cancels.cancel(tenant.id, jobId())

        val output = verdict(verdict, failing = verdict == VerifierVerdict.FAILED)
        results.record(tenant.id, verifier, stepId(), StepReport(step, "verifier says", output))
        loop.tick()

        assertEquals(status, records().single()["status"])
        assertEquals("finished_first", jobs().single()["cancel_result"])
        assertEquals("closed", jobs().single()["status"])
        assertNull(records().single()["cancelled_by"])
    }

    @Test
    fun `a passed verdict that came before the cancel stays the result of the job`() =
        verdictBeforeCancel(VerifierVerdict.PASSED, StepState.SUCCEEDED, "passed")

    @Test
    fun `a failed verdict that came before the cancel stays the result of the job`() =
        verdictBeforeCancel(VerifierVerdict.FAILED, StepState.FAILED, "failed")

    @Test
    fun `a cannot verify verdict that came before the cancel stays the result of the job`() =
        verdictBeforeCancel(VerifierVerdict.CANNOT_VERIFY, StepState.FAILED, "cannot_verify")

    @Test
    fun `a verifier that does not come back within the lost window closes the job as cancelled without a retry`() {
        jobWithRunningAttempt()
        cancels.cancel(tenant.id, jobId())
        jdbc.update("update run_steps set lost_deadline = ? where id = ?", Timestamp.from(clock.now), stepId())
        clock.now = clock.now + Duration.ofSeconds(60)

        assertTrue(steps.lost(tenant.id, stepId()))
        loop.tick()

        val attempt = attempt()
        assertEquals("cancelled", attempt["run_status"])
        assertEquals(CANCEL_UNCONFIRMED_MESSAGE, attempt["message"])
        assertEquals("closed", jobs().single()["status"])
        assertEquals("unconfirmed", jobs().single()["cancel_result"])
        val record = records().single()
        assertEquals("cancelled", record["status"])
        assertEquals(CANCEL_UNCONFIRMED_MESSAGE, record["reason"])
        assertEquals(1, attempts())
    }

    // --- what is refused, and what comes after

    @Test
    fun `a closed job is refused and its record does not change`() {
        val verifier = jobWithRunningAttempt()
        val job = jobId()
        results.record(
            tenant.id,
            verifier,
            stepId(),
            StepReport(StepState.SUCCEEDED, null, verdict(VerifierVerdict.PASSED)),
        )
        loop.tick()
        val before = records()

        assertFailsWith<RunAlreadyFinished> { cancels.cancel(tenant.id, job) }

        assertEquals(before, records())
        assertEquals(listOf("passed"), records().map { it["status"] })
        assertNull(jobs().single()["cancel_requested_at"])
    }

    @Test
    fun `a verdict that arrived but was not yet settled wins, and the cancel is refused`() {
        val verifier = jobWithRunningAttempt()
        val job = jobId()
        results.record(
            tenant.id,
            verifier,
            stepId(),
            StepReport(StepState.SUCCEEDED, null, verdict(VerifierVerdict.PASSED)),
        )

        assertFailsWith<RunAlreadyFinished> { cancels.cancel(tenant.id, job) }

        assertEquals(listOf("passed"), records().map { it["status"] })
        assertEquals("closed", jobs().single()["status"])
        assertEquals(emptyList(), told.calls)
    }

    @Test
    fun `a job of another tenant or none at all is not found`() {
        val other = RunsTenant(jdbc).create()
        try {
            jobWithRunningAttempt()

            assertFailsWith<VerificationJobNotFound> { cancels.cancel(other.id, jobId()) }
            assertFailsWith<VerificationJobNotFound> { cancels.cancel(tenant.id, UUID.randomUUID()) }
            assertFailsWith<RunNotFound> { runCancels.cancel(other.id, attempt()["run_id"] as UUID) }

            assertNull(jobs().single()["cancel_requested_at"])
            assertEquals(emptyList(), told.calls)
        } finally {
            other.drop()
        }
    }

    @Test
    fun `an attempt run of a closed job answers run finished`() {
        val verifier = jobWithRunningAttempt()
        val run = attempt()["run_id"] as UUID
        results.record(
            tenant.id,
            verifier,
            stepId(),
            StepReport(StepState.SUCCEEDED, null, verdict(VerifierVerdict.PASSED)),
        )
        loop.tick()

        assertFailsWith<RunAlreadyFinished> { runCancels.cancel(tenant.id, run) }
    }

    @Test
    fun `a cancelled cycle job makes no new job in the same cycle, and verify now works at once`() {
        val verifier = jobWithRunningAttempt()
        cancels.cancel(tenant.id, jobId())
        cancelledReport(verifier)
        loop.tick()
        loop.tick()

        assertEquals(listOf("closed"), jobs().map { it["status"] })

        val manual = verifications.verifyNow(tenant.id, sourceId, null)

        assertNotNull(manual.id)
        assertEquals(listOf("closed", "queued"), jobs().map { it["status"] as String }.sorted())
    }

    // --- the history

    /** Writes a record of [status] through the history, as the end of a job of the source would. */
    private fun record(status: VerificationStatus) {
        val snapshot =
            jdbc.queryForMap("select id, repository_id from snapshots where tenant_id = ? limit 1", tenant.id)
        val job = UUID.randomUUID()
        val now = Timestamp.from(clock.now)
        jdbc.update(
            "insert into verification_jobs (id, tenant_id, source_id, snapshot_ref, repository_id, plugin, reason, " +
                "status, created_at, deadline, closed_at) values (?, ?, ?, ?, ?, 'files', 'manual', 'closed', ?, ?, ?)",
            job,
            tenant.id,
            sourceId,
            snapshot["id"],
            snapshot["repository_id"],
            now,
            now,
            now,
        )
        val sql = "select id from run_steps where tenant_id = ? limit 1"
        val step = jdbc.queryForObject(sql, UUID::class.java, tenant.id)
        val cancelled = status == VerificationStatus.CANCELLED
        val entry =
            VerificationEntry(
                job,
                VerifiedSnapshot(sourceId, snapshot["id"] as UUID, snapshot["repository_id"] as String),
                status,
                if (status == VerificationStatus.PASSED) 5 else 0,
                null,
                stepId = step,
                verifierId = tenant.agentId,
                cancelledBy = "admin".takeIf { cancelled },
                cancelledAt = clock.now.takeIf { cancelled },
            )
        sessions.inTenant(tenant.id) { history.record(it, tenant.id, entry, clock.now) }
        clock.now = clock.now + LATER
    }

    @Test
    fun `a pass after a failure and a cancelled verification is a recovery`() {
        record(VerificationStatus.FAILED)
        record(VerificationStatus.CANCELLED)
        record(VerificationStatus.PASSED)

        assertEquals(listOf(false, false, true), records().map { it["recovered"] })
    }

    @Test
    fun `a pass after a cancelled verification alone is not a recovery`() {
        record(VerificationStatus.CANCELLED)
        record(VerificationStatus.PASSED)

        assertEquals(listOf(false, false), records().map { it["recovered"] })
    }
}
