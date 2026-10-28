// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.runs.CancelResult
import dev.sard.server.runs.StepState
import dev.sard.server.runs.VerificationRuns
import org.hibernate.Session
import org.slf4j.LoggerFactory
import java.time.Instant
import java.util.UUID

private val log = LoggerFactory.getLogger(VerificationSettlement::class.java)

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). A job whose current attempt's step has ended; the job's
// cancel mark and how the attempt's run ended its own (J1f).
private const val ENDED = """
    select j.id, j.source_id, j.snapshot_ref, j.repository_id, j.failed_attempts, st.id, st.agent_id, st.status,
        st.message, cast(st.output as text), j.cancel_requested_at, j.cancel_requested_by, r.cancel_result
    from verification_jobs j
    join runs r on r.tenant_id = j.tenant_id and r.id = j.run_id
    join run_steps st on st.tenant_id = j.tenant_id and st.run_id = j.run_id
    where j.tenant_id = :tenant and j.status = 'running' and st.status not in ('queued', 'dispatched', 'running')"""
private const val ALL_ENDED = "$ENDED order by j.created_at, j.id for update of j skip locked"
private const val ONE_ENDED = "$ENDED and j.id = :job for update of j"
private const val REQUEUE =
    "update verification_jobs set status = 'queued', failed_attempts = :failed where tenant_id = :tenant and id = :id"
private const val CANCEL_RESULT =
    "update verification_jobs set cancel_result = :result where tenant_id = :tenant and id = :id"

/** A row of ENDED: the columns after the job's first five. */
private class Ended(
    row: Array<Any?>,
) {
    val jobId = row[0] as UUID
    val snapshot = VerifiedSnapshot(row[1] as UUID, row[2] as UUID, row[3] as String)
    val failedAttempts = (row[4] as Number).toInt()
    val stepId = row[5] as UUID
    val verifierId = row[6] as UUID
    val status = StepState.of(row[7] as String)
    val message = row[8] as String?
    val output = VerificationRuns.outputOf(row[9] as String?)
    val cancelRequestedAt = row[10] as Instant?
    val cancelRequestedBy = row[11] as String?
    val runCancelResult = (row[12] as String?)?.let(CancelResult::of)
}

/**
 * Reads the end of each running job's attempt from its step (V4 «Результаты»): a verdict closes the job and
 * records it; an attempt without one (lost — FXs —, rejected, timed out) sends the job back to the queue, for the
 * same or another verifier, and the third in a row records an infrastructure failure. It reads the step, not the
 * RunFinished event, so a server that stopped between the two still settles the job on its next tick. A job that a
 * person cancelled (J1f) is never sent back: a verdict that came first is its result, anything else records
 * `cancelled`.
 */
class VerificationSettlement(
    private val history: VerificationHistory,
) {
    fun settle(
        session: Session,
        tenantId: UUID,
        now: Instant,
    ) {
        val ended =
            session
                .createNativeQuery(ALL_ENDED.trimIndent(), Array<Any?>::class.java)
                .setParameter("tenant", tenantId)
                .list()
        for (row in ended) settle(session, tenantId, Ended(row), now)
    }

    /** Settles [jobId] if its attempt has ended; true when it did (the job is closed or queued again). */
    fun settleJob(
        session: Session,
        tenantId: UUID,
        jobId: UUID,
        now: Instant,
    ): Boolean {
        val ended =
            session
                .createNativeQuery(ONE_ENDED.trimIndent(), Array<Any?>::class.java)
                .setParameter("tenant", tenantId)
                .setParameter("job", jobId)
                .list()
        for (row in ended) settle(session, tenantId, Ended(row), now)
        return ended.isNotEmpty()
    }

    private fun settle(
        session: Session,
        tenantId: UUID,
        ended: Ended,
        now: Instant,
    ) {
        val cancelRequested = ended.cancelRequestedAt != null
        val outcome =
            AttemptOutcome.of(ended.status, ended.message, ended.output, ended.failedAttempts, cancelRequested)
        when (outcome) {
            is AttemptOutcome.Retry -> {
                log.info("verification job {}: attempt ended {}, back in the queue", ended.jobId, ended.status.stored)
                requeue(session, tenantId, ended.jobId, outcome.failedAttempts)
            }

            is AttemptOutcome.Settled -> {
                finish(session, tenantId, ended, outcome, now)
            }
        }
    }

    /** The job is done: its record, how its cancel ended if there was one, and the job closed. */
    private fun finish(
        session: Session,
        tenantId: UUID,
        ended: Ended,
        outcome: AttemptOutcome.Settled,
        now: Instant,
    ) {
        val cancelled = outcome.status == VerificationStatus.CANCELLED
        val entry =
            VerificationEntry(
                jobId = ended.jobId,
                snapshot = ended.snapshot,
                status = outcome.status,
                level = outcome.level,
                reason = outcome.reason,
                stepId = ended.stepId,
                verifierId = ended.verifierId,
                output = ended.output,
                cancelledBy = ended.cancelRequestedBy.takeIf { cancelled },
                cancelledAt = ended.cancelRequestedAt.takeIf { cancelled },
            )
        history.record(session, tenantId, entry, now)
        if (ended.cancelRequestedAt != null) recordCancelResult(session, tenantId, ended, cancelled)
        close(session, tenantId, ended.jobId, now)
    }

    /** How the job's cancel ended: the attempt's own result when it was cancelled, else the verdict came first. */
    private fun recordCancelResult(
        session: Session,
        tenantId: UUID,
        ended: Ended,
        cancelled: Boolean,
    ) {
        val result =
            when {
                !cancelled -> CancelResult.FINISHED_FIRST
                ended.runCancelResult == CancelResult.UNCONFIRMED -> CancelResult.UNCONFIRMED
                else -> CancelResult.CANCELLED
            }
        session
            .createNativeMutationQuery(CANCEL_RESULT)
            .setParameter("result", result.stored)
            .setParameter("tenant", tenantId)
            .setParameter("id", ended.jobId)
            .executeUpdate()
    }

    private fun close(
        session: Session,
        tenantId: UUID,
        jobId: UUID,
        now: Instant,
    ) = session
        .createNativeMutationQuery(CLOSE)
        .setParameter("now", now)
        .setParameter("tenant", tenantId)
        .setParameter("id", jobId)
        .executeUpdate()

    private fun requeue(
        session: Session,
        tenantId: UUID,
        jobId: UUID,
        failed: Int,
    ) = session
        .createNativeMutationQuery(REQUEUE)
        .setParameter("failed", failed.toShort())
        .setParameter("tenant", tenantId)
        .setParameter("id", jobId)
        .executeUpdate()
}
