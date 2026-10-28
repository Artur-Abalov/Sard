// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.runs.ADMIN
import dev.sard.server.runs.AttemptCancels
import dev.sard.server.runs.CancelDone
import dev.sard.server.runs.CancelMarks
import dev.sard.server.runs.RunAlreadyFinished
import dev.sard.server.runs.Runs
import org.hibernate.Session
import java.time.Clock
import java.time.Instant
import java.util.UUID

// Native SQL names tenant_id explicitly (ADR 0013, rule 8); every WHERE names the status it expects.
private const val LOCK = """
    select status, run_id, source_id, snapshot_ref, repository_id, cancel_requested_at
    from verification_jobs where tenant_id = :tenant and id = :id for update"""
private const val OF_RUN = "select id from verification_jobs where tenant_id = :tenant and run_id = :run"
private const val IS_ATTEMPT =
    "select true from runs where tenant_id = :tenant and id = :run and trigger = 'verification'"
private const val MARK = """
    update verification_jobs set cancel_requested_at = :now, cancel_requested_by = :by, cancel_result = 'pending'
    where tenant_id = :tenant and id = :id and status <> 'closed' and cancel_requested_at is null"""
private const val QUEUED_RESULT =
    "update verification_jobs set cancel_result = 'cancelled' where tenant_id = :tenant and id = :id"
private const val NOT_TAKEN = "cancelled before a verifier took the job"

/** No verification job with this id in the caller's tenant (404). */
class VerificationJobNotFound(
    val jobId: UUID,
) : RuntimeException("no verification job $jobId")

/** A job as the cancel finds it, locked. */
private class Locked(
    row: Array<Any?>,
    val id: UUID,
) {
    val running = row[0] == "running"
    val closed = row[0] == "closed"
    val runId = row[1] as UUID?
    val sourceId = row[2] as UUID
    val snapshot = VerifiedSnapshot(sourceId, row[3] as UUID, row[4] as String)
}

/** What the transaction of a cancel did, for what follows its commit. */
private class Cancelled(
    val sourceId: UUID,
    /** The job was final, or settled with its verdict, before the cancel: nothing was cancelled. */
    val finished: Boolean,
    val runId: UUID?,
    val done: CancelDone,
)

/**
 * Cancels a restore verification job on an administrator's request (J1f, ВJf1): a job in the queue closes at once with
 * a record `cancelled`; a job with an attempt gets the cancel mark and its attempt is cancelled as any run is, so the
 * verifier is sent a CancelStep; the end of the attempt, or the lost window, closes the job (VerificationSettlement).
 * A verdict that already came wins: the job settles first and the cancel is refused (`run_finished`).
 */
class VerificationCancels(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val runs: Runs,
    private val marks: CancelMarks,
    private val settlement: VerificationSettlement,
    private val history: VerificationHistory,
) : AttemptCancels {
    /** The id of the job's source; throws [VerificationJobNotFound], or [RunAlreadyFinished] for a closed job. */
    fun cancel(
        tenantId: UUID,
        jobId: UUID,
    ): UUID {
        val cancelled = sessions.inTenant(tenantId) { attempt(it, tenantId, jobId, clock.instant()) }
        if (cancelled.finished) throw RunAlreadyFinished(jobId)
        cancelled.runId?.let { run -> runs.get(tenantId, run)?.let { marks.told(tenantId, it, cancelled.done) } }
        return cancelled.sourceId
    }

    /** The attempt's job is cancelled; false for a run that is not an attempt of a verification job. */
    override fun cancelAttempt(
        tenantId: UUID,
        runId: UUID,
    ): Boolean {
        val (attempt, job) =
            sessions.inTenant(tenantId) { isAttempt(it, tenantId, runId) to jobOf(it, tenantId, runId) }
        if (!attempt) return false
        cancel(tenantId, job ?: throw RunAlreadyFinished(runId))
        return true
    }

    private fun attempt(
        session: Session,
        tenantId: UUID,
        jobId: UUID,
        now: Instant,
    ): Cancelled {
        val job = settledFirst(session, tenantId, jobId, now)
        if (job.closed) return Cancelled(job.sourceId, true, null, CancelDone.NOTHING)
        session
            .createNativeMutationQuery(MARK)
            .setParameter("now", now)
            .setParameter("by", ADMIN)
            .setParameter("tenant", tenantId)
            .setParameter("id", jobId)
            .executeUpdate()
        val attempt = job.runId.takeIf { job.running }?.let { cancelAttempt(session, tenantId, job, it, now) }
        return attempt ?: closeQueued(session, tenantId, job, now)
    }

    /** The job; one whose attempt has ended is settled first: a verdict that came first is the job's result. */
    private fun settledFirst(
        session: Session,
        tenantId: UUID,
        jobId: UUID,
        now: Instant,
    ): Locked {
        val job = lock(session, tenantId, jobId)
        val settled = job.running && settlement.settleJob(session, tenantId, jobId, now)
        return if (settled) lock(session, tenantId, jobId) else job
    }

    /** The attempt is cancelled as any run is; one that was still queued ends at once and settles the job. */
    private fun cancelAttempt(
        session: Session,
        tenantId: UUID,
        job: Locked,
        runId: UUID,
        now: Instant,
    ): Cancelled {
        val done = marks.apply(session, tenantId, runId)
        if (done == CancelDone.CLOSED) settlement.settleJob(session, tenantId, job.id, now)
        return Cancelled(job.sourceId, false, runId, done)
    }

    /** A job nobody took: no attempt to stop, so it is closed here with its record. */
    private fun closeQueued(
        session: Session,
        tenantId: UUID,
        job: Locked,
        now: Instant,
    ): Cancelled {
        val entry =
            VerificationEntry(
                job.id,
                job.snapshot,
                VerificationStatus.CANCELLED,
                0,
                NOT_TAKEN,
                cancelledBy = ADMIN,
                cancelledAt = now,
            )
        history.record(session, tenantId, entry, now)
        closeJob(session, tenantId, job.id, now)
        session
            .createNativeMutationQuery(QUEUED_RESULT)
            .setParameter("tenant", tenantId)
            .setParameter("id", job.id)
            .executeUpdate()
        return Cancelled(job.sourceId, false, null, CancelDone.NOTHING)
    }

    private fun lock(
        session: Session,
        tenantId: UUID,
        jobId: UUID,
    ): Locked =
        session
            .createNativeQuery(LOCK, Array<Any?>::class.java)
            .setParameter("tenant", tenantId)
            .setParameter("id", jobId)
            .list()
            .firstOrNull()
            ?.let { Locked(it, jobId) } ?: throw VerificationJobNotFound(jobId)

    private fun isAttempt(
        session: Session,
        tenantId: UUID,
        runId: UUID,
    ): Boolean =
        session
            .createNativeQuery(IS_ATTEMPT, Boolean::class.javaObjectType)
            .setParameter("tenant", tenantId)
            .setParameter("run", runId)
            .list()
            .isNotEmpty()

    /** The job whose attempt is [runId], current or past; null for none. */
    private fun jobOf(
        session: Session,
        tenantId: UUID,
        runId: UUID,
    ): UUID? =
        session
            .createNativeQuery(OF_RUN, UUID::class.java)
            .setParameter("tenant", tenantId)
            .setParameter("run", runId)
            .list()
            .firstOrNull()
}
