// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.RunStepRecord
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import org.hibernate.Session
import org.slf4j.LoggerFactory
import java.time.Clock
import java.util.UUID

private val log = LoggerFactory.getLogger(StepResults::class.java)

private const val OWN_STEP = "from RunStepRecord where id = :step and agentId = :agent"
private const val STATUS =
    "select s.status, r.cancel_result from run_steps s join runs r on r.tenant_id = s.tenant_id and r.id = s.run_id " +
        "where s.tenant_id = :tenant and s.id = :step"

// What the agent sent after the server closed the step as cancelled (its agent did not confirm the cancel, J1a):
// its status is kept once, with the output when there is one; the step's and the run's status stay.
private const val KEEP_LATE_CANCELLED =
    "update run_steps set late_status = :late, output = cast(:output as jsonb) " +
        "where tenant_id = :tenant and id = :step and status = 'cancelled' and late_status is null"
private const val KEEP_LATE =
    "update run_steps set output = cast(:output as jsonb) " +
        "where tenant_id = :tenant and id = :step and status = 'lost' and output is null"
private const val SNAPSHOT =
    "insert into snapshots (id, tenant_id, source_id, step_id, agent_id, repository_name, repository_id, " +
        "snapshot_id, total_bytes, added_bytes, created_at, partial, fingerprint, fingerprint_sha256, total_files) " +
        "values (:id, :tenant, :source, :step, :agent, :repositoryName, :repositoryId, :snapshotId, :totalBytes, " +
        ":addedBytes, :now, :partial, :fingerprint, :fingerprintSha256, :totalFiles) on conflict do nothing"

// The last whole snapshot of the source: what a fall of the volume is measured from. Its number of files is null
// when an agent older than V2 made it; its size is compared all the same.
private const val PREVIOUS =
    "select total_files, total_bytes from snapshots where tenant_id = :tenant and source_id = :source " +
        "and not partial and forgotten_at is null order by created_at desc, id desc limit 1"

/** What [StepResults.record] did with a result; every case but an exception is acknowledged. */
sealed interface Recorded {
    /** This result closed the step (and its run) with [status]; [invalid] says why it is not the agent's. */
    data class Closed(
        val status: StepState,
        val invalid: String?,
    ) : Recorded

    /** The step was already closed by an earlier copy of the result: nothing written. */
    data object Repeated : Recorded

    /** The step was lost before its result came; it stays lost, [kept] says whether its output was stored now. */
    data class Late(
        val kept: Boolean,
    ) : Recorded

    /** No step of this agent has this id, in its tenant: another agent's, another tenant's, or none. */
    data object Unknown : Recorded

    /** The step was never sent, so this agent cannot have run it: nothing written. */
    data object NotDispatched : Recorded
}

/**
 * Records a StepResult (S7a): in one transaction of the agent's tenant, the step's final status
 * (the guarded transition of [StepTransitions]), its output, the run and, for a succeeded backup,
 * its snapshot. [RunFinished] is published after the commit. A database failure throws and leaves
 * nothing, so the caller must not acknowledge the result: the agent sends it again.
 */
class StepResults(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val ids: UuidV7,
    private val transitions: StepTransitions,
    private val announcer: RunAnnouncer,
) {
    fun record(
        tenantId: UUID,
        agentId: UUID,
        stepId: UUID,
        report: StepReport,
    ): Recorded {
        val recorded = sessions.inTenant(tenantId) { record(it, tenantId, agentId, stepId, report) }
        if (recorded is Recorded.Closed) announcer.announce(tenantId, stepId)
        return recorded
    }

    private fun record(
        session: Session,
        tenantId: UUID,
        agentId: UUID,
        stepId: UUID,
        report: StepReport,
    ): Recorded {
        val step =
            session
                .createSelectionQuery(OWN_STEP, RunStepRecord::class.java)
                .setParameter("step", stepId)
                .setParameter("agent", agentId)
                .uniqueResult() ?: return Recorded.Unknown
        val verdict = withWarnings(session, step, ResultCheck.of(Action.of(step.action), report))
        return if (transitions.finish(session, tenantId, stepId, verdict.outcome, verdict.output?.json())) {
            snapshot(session, step, verdict)
            Recorded.Closed(verdict.outcome.status, verdict.invalid)
        } else {
            closedBefore(session, tenantId, step, verdict)
        }
    }

    /** A backup much smaller than the last snapshot of its source gets the warnings of [VolumeDrop] (V2). */
    private fun withWarnings(
        session: Session,
        step: RunStepRecord,
        verdict: Verdict,
    ): Verdict {
        val backup = verdict.output as? StepOutput.Backup
        backup ?: return verdict
        val warnings = VolumeDrop.warnings(previous(session, step), backup.totals())
        return verdict.copy(output = backup.copy(warnings = warnings))
    }

    /** What restic counted in the last whole snapshot of the source of [step], if it is known. */
    private fun previous(
        session: Session,
        step: RunStepRecord,
    ): SnapshotTotals? =
        session
            .createNativeQuery(PREVIOUS, Array<Any?>::class.java)
            .setParameter("tenant", step.tenantId)
            .setParameter("source", step.sourceId)
            .uniqueResult()
            ?.let { SnapshotTotals((it[0] as Number?)?.toLong(), (it[1] as Number).toLong()) }

    /** The guarded update moved nothing: the step was closed before, or never dispatched. */
    private fun closedBefore(
        session: Session,
        tenantId: UUID,
        step: RunStepRecord,
        verdict: Verdict,
    ): Recorded =
        closed(session, tenantId, step.id).let { (state, cancel) ->
            when (state) {
                StepState.LOST -> Recorded.Late(keepLate(session, step, verdict))
                StepState.QUEUED -> Recorded.NotDispatched
                StepState.CANCELLED -> afterCancel(session, step, verdict, cancel)
                else -> Recorded.Repeated
            }
        }

    /**
     * A step cancelled whose agent never confirmed it keeps what the agent sends later (J1a, Р-J8); one the agent
     * confirmed, or cancelled in the queue, had its result already.
     */
    private fun afterCancel(
        session: Session,
        step: RunStepRecord,
        verdict: Verdict,
        cancelResult: String?,
    ): Recorded {
        if (cancelResult != CancelResult.UNCONFIRMED.stored) return Recorded.Repeated
        return keepLateCancelled(session, step, verdict)
    }

    private fun keepLateCancelled(
        session: Session,
        step: RunStepRecord,
        verdict: Verdict,
    ): Recorded {
        val output = verdict.output
        val moved =
            session
                .createNativeMutationQuery(KEEP_LATE_CANCELLED)
                .setParameter("late", verdict.outcome.status.stored)
                .setParameter("output", output?.json(), String::class.java)
                .setParameter("tenant", step.tenantId)
                .setParameter("step", step.id)
                .executeUpdate() == 1
        if (moved) snapshot(session, step, verdict)
        return Recorded.Late(moved && output != null)
    }

    /** The step's status, and how the cancel of its run ended if there was one. */
    private fun closed(
        session: Session,
        tenantId: UUID,
        stepId: UUID,
    ): Pair<StepState, String?> {
        val row =
            session
                .createNativeQuery(STATUS, Array<Any?>::class.java)
                .setParameter("tenant", tenantId)
                .setParameter("step", stepId)
                .singleResult
        return StepState.of(row[0] as String) to row[1] as String?
    }

    /** A lost step keeps its status; the output of its late result is stored once (S7a, answer 1). */
    private fun keepLate(
        session: Session,
        step: RunStepRecord,
        verdict: Verdict,
    ): Boolean {
        val output = verdict.output ?: return false
        val kept =
            session
                .createNativeMutationQuery(KEEP_LATE)
                .setParameter("output", output.json(), String::class.java)
                .setParameter("tenant", step.tenantId)
                .setParameter("step", step.id)
                .executeUpdate() == 1
        if (kept) snapshot(session, step, verdict)
        return kept
    }

    private fun snapshot(
        session: Session,
        step: RunStepRecord,
        verdict: Verdict,
    ) {
        val backup = verdict.output as? StepOutput.Backup ?: return
        val inserted =
            session
                .createNativeMutationQuery(SNAPSHOT)
                .setParameter("id", ids.next())
                .setParameter("tenant", step.tenantId)
                .setParameter("source", step.sourceId)
                .setParameter("step", step.id)
                .setParameter("agent", step.agentId)
                .setParameter("repositoryName", step.repositoryName)
                .setParameter("repositoryId", backup.repositoryId)
                .setParameter("snapshotId", backup.snapshotId)
                .setParameter("totalBytes", backup.totalBytes)
                .setParameter("addedBytes", backup.addedBytes)
                .setParameter("now", clock.instant())
                .setParameter("partial", backup.partial)
                .setParameter("fingerprint", backup.fingerprint?.bytes, ByteArray::class.java)
                .setParameter("fingerprintSha256", backup.fingerprint?.sha256, String::class.java)
                .setParameter("totalFiles", backup.totalFiles, Long::class.javaObjectType)
                .executeUpdate()
        if (inserted == 0) {
            log.warn("snapshot {} of step {} is already recorded for another step", backup.snapshotId, step.id)
        }
    }
}
