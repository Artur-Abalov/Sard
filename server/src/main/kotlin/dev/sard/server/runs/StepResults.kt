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
    "select s.status, r.cancel_result, s.timed_out_by from run_steps s " +
        "join runs r on r.tenant_id = s.tenant_id and r.id = s.run_id where s.tenant_id = :tenant and s.id = :step"

// What the agent sent after the server closed the step (R2 ВВ11, J1a, J1b): the step was lost, timed out by the
// server, or cancelled without its agent's confirmation. Its status is kept once, with the output when there is one;
// the step's and the run's status stay.
private const val KEEP_LATE =
    "update run_steps set late_status = :late, output = cast(:output as jsonb) " +
        "where tenant_id = :tenant and id = :step and late_status is null and output is null and " +
        "(status = 'lost' or (status = 'timed_out' and timed_out_by = 'server') or (status = 'cancelled' and " +
        "exists (select 1 from runs r where r.tenant_id = run_steps.tenant_id and r.id = run_steps.run_id " +
        "and r.cancel_result = 'unconfirmed')))"
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

    /**
     * The server had closed the step (lost, timed out by the server or cancelled unconfirmed) before its result came;
     * it keeps its status, [kept] says whether the output was stored now.
     */
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
    ): Recorded {
        val closed = closed(session, tenantId, step.id)
        return when {
            closed.status == StepState.QUEUED -> Recorded.NotDispatched
            closed.byServer -> keepLate(session, step, verdict)
            else -> Recorded.Repeated
        }
    }

    /** How the server found the step: its status, how the cancel of its run ended and who timed it out. */
    private data class Closed(
        val status: StepState,
        val cancelResult: String?,
        val timedOutBy: String?,
    ) {
        /** The server closed it, so a result of its agent can still come (J1a, J1b, R2 ВВ11). */
        val byServer
            get() =
                when (status) {
                    StepState.LOST -> true
                    StepState.CANCELLED -> cancelResult == CancelResult.UNCONFIRMED.stored
                    StepState.TIMED_OUT -> timedOutBy == TimedOutBy.SERVER.stored
                    else -> false
                }
    }

    private fun closed(
        session: Session,
        tenantId: UUID,
        stepId: UUID,
    ): Closed {
        val row =
            session
                .createNativeQuery(STATUS, Array<Any?>::class.java)
                .setParameter("tenant", tenantId)
                .setParameter("step", stepId)
                .singleResult
        return Closed(StepState.of(row[0] as String), row[1] as String?, row[2] as String?)
    }

    /** The late result of a step the server closed: its status and output are kept once, and a backup's snapshot. */
    private fun keepLate(
        session: Session,
        step: RunStepRecord,
        verdict: Verdict,
    ): Recorded {
        val output = verdict.output
        val moved =
            session
                .createNativeMutationQuery(KEEP_LATE)
                .setParameter("late", verdict.outcome.status.stored)
                .setParameter("output", output?.json(), String::class.java)
                .setParameter("tenant", step.tenantId)
                .setParameter("step", step.id)
                .executeUpdate() == 1
        if (moved) snapshot(session, step, verdict)
        return Recorded.Late(moved && output != null)
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
