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
private const val STATUS = "select status from run_steps where tenant_id = :tenant and id = :step"
private const val KEEP_LATE =
    "update run_steps set output = cast(:output as jsonb) " +
        "where tenant_id = :tenant and id = :step and status = 'lost' and output is null"
private const val SNAPSHOT =
    "insert into snapshots (id, tenant_id, source_id, step_id, agent_id, repository_name, repository_id, " +
        "snapshot_id, total_bytes, added_bytes, created_at, partial) values (:id, :tenant, :source, :step, :agent, " +
        ":repositoryName, :repositoryId, :snapshotId, :totalBytes, :addedBytes, :now, :partial) on conflict do nothing"

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
        val verdict = ResultCheck.of(Action.of(step.action), report)
        return if (transitions.finish(session, tenantId, stepId, verdict.outcome, verdict.output?.json())) {
            snapshot(session, step, verdict)
            Recorded.Closed(verdict.outcome.status, verdict.invalid)
        } else {
            closedBefore(session, tenantId, step, verdict)
        }
    }

    /** The guarded update moved nothing: the step was closed before, or never dispatched. */
    private fun closedBefore(
        session: Session,
        tenantId: UUID,
        step: RunStepRecord,
        verdict: Verdict,
    ): Recorded =
        when (status(session, tenantId, step.id)) {
            StepState.LOST -> Recorded.Late(keepLate(session, step, verdict))
            StepState.QUEUED -> Recorded.NotDispatched
            else -> Recorded.Repeated
        }

    private fun status(
        session: Session,
        tenantId: UUID,
        stepId: UUID,
    ): StepState {
        val stored =
            session
                .createNativeQuery(STATUS, String::class.java)
                .setParameter("tenant", tenantId)
                .setParameter("step", stepId)
                .singleResult
        return StepState.of(stored)
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
                .executeUpdate()
        if (inserted == 0) {
            log.warn("snapshot {} of step {} is already recorded for another step", backup.snapshotId, step.id)
        }
    }
}
