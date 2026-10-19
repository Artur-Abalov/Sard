// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.runs.StepState
import dev.sard.server.runs.VerificationRuns
import org.hibernate.Session
import org.slf4j.LoggerFactory
import java.time.Instant
import java.util.UUID

private val log = LoggerFactory.getLogger(VerificationSettlement::class.java)

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). A job whose current attempt's step has ended.
private const val ENDED = """
    select j.id, j.source_id, j.snapshot_ref, j.repository_id, j.failed_attempts, st.id, st.agent_id, st.status,
        st.message, cast(st.output as text)
    from verification_jobs j
    join run_steps st on st.tenant_id = j.tenant_id and st.run_id = j.run_id
    where j.tenant_id = :tenant and j.status = 'running' and st.status not in ('queued', 'dispatched', 'running')
    order by j.created_at, j.id
    for update of j skip locked"""
private const val REQUEUE =
    "update verification_jobs set status = 'queued', failed_attempts = :failed where tenant_id = :tenant and id = :id"

/**
 * Reads the end of each running job's attempt from its step (V4 «Результаты»): a verdict closes the job and
 * records it; an attempt without one (lost — FXs —, rejected, timed out) sends the job back to the queue, for the
 * same or another verifier, and the third in a row records an infrastructure failure. It reads the step, not the
 * RunFinished event, so a server that stopped between the two still settles the job on its next tick.
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
                .createNativeQuery(ENDED.trimIndent(), Array<Any?>::class.java)
                .setParameter("tenant", tenantId)
                .list()
        for (row in ended) settle(session, tenantId, row, now)
    }

    private fun settle(
        session: Session,
        tenantId: UUID,
        row: Array<Any?>,
        now: Instant,
    ) {
        val jobId = row[0] as UUID
        val status = StepState.of(row[7] as String)
        val output = VerificationRuns.outputOf(row[9] as String?)
        when (val outcome = AttemptOutcome.of(status, row[8] as String?, output, (row[4] as Number).toInt())) {
            is AttemptOutcome.Retry -> {
                log.info("verification job {}: attempt ended {}, back in the queue", jobId, status.stored)
                requeue(session, tenantId, jobId, outcome.failedAttempts)
            }

            is AttemptOutcome.Settled -> {
                val snapshot = VerifiedSnapshot(row[1] as UUID, row[2] as UUID, row[3] as String)
                val entry =
                    VerificationEntry(
                        jobId = jobId,
                        snapshot = snapshot,
                        status = outcome.status,
                        level = outcome.level,
                        reason = outcome.reason,
                        stepId = row[5] as UUID,
                        verifierId = row[6] as UUID,
                        output = output,
                    )
                history.record(session, tenantId, entry, now)
                session
                    .createNativeMutationQuery(CLOSE)
                    .setParameter("now", now)
                    .setParameter("tenant", tenantId)
                    .setParameter("id", jobId)
                    .executeUpdate()
            }
        }
    }

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
