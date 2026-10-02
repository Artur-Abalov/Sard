// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.TenantSessions
import java.util.UUID

private const val OWN_STATUS =
    "select status from run_steps where tenant_id = :tenant and id = :step and agent_id = :agent"

// A value the agent does not know (null) keeps the last one; only a running step moves.
private const val UPDATE =
    "update run_steps set phase = coalesce(cast(:phase as text), phase), " +
        "bytes_processed = coalesce(cast(:processed as bigint), bytes_processed), " +
        "bytes_total = coalesce(cast(:total as bigint), bytes_total), " +
        "files_processed = coalesce(cast(:filesProcessed as bigint), files_processed), " +
        "files_total = coalesce(cast(:filesTotal as bigint), files_total) " +
        "where tenant_id = :tenant and id = :step and status = 'running'"

/** A StepProgress as the domain sees it; null is "unknown" (proto: UNSPECIFIED phase, a total of 0). */
data class ProgressReport(
    val phase: String?,
    val bytesProcessed: Long?,
    val bytesTotal: Long?,
    val filesProcessed: Long? = null,
    val filesTotal: Long? = null,
)

enum class ProgressWritten {
    /** The first progress: dispatched → running (S6a's guarded `accepted`), run started. */
    STARTED,

    /** Phase and counters of a running step. */
    UPDATED,

    /** The step is queued or final: nothing written. */
    CLOSED,

    /** No step of this agent has this id, in its tenant. */
    UNKNOWN,
}

/** Writes progress of an agent's own step (S7a); how often is [ProgressThrottle]'s business. */
class StepProgressWrites(
    private val sessions: TenantSessions,
    private val transitions: StepTransitions,
) {
    fun write(
        tenantId: UUID,
        agentId: UUID,
        stepId: UUID,
        report: ProgressReport,
    ): ProgressWritten {
        val status = status(tenantId, agentId, stepId) ?: return ProgressWritten.UNKNOWN
        val started = start(tenantId, stepId, status, report)
        val updated = update(tenantId, stepId, report)
        return if (started) {
            ProgressWritten.STARTED
        } else if (updated) {
            ProgressWritten.UPDATED
        } else {
            ProgressWritten.CLOSED
        }
    }

    /** dispatched → running on the first progress, through S6a's guarded transition. */
    private fun start(
        tenantId: UUID,
        stepId: UUID,
        status: StepState,
        report: ProgressReport,
    ) = status == StepState.DISPATCHED && transitions.accepted(tenantId, stepId, report.phase ?: ACCEPTED)

    private fun status(
        tenantId: UUID,
        agentId: UUID,
        stepId: UUID,
    ): StepState? =
        sessions.inTenant(tenantId) { session ->
            session
                .createNativeQuery(OWN_STATUS, String::class.java)
                .setParameter("tenant", tenantId)
                .setParameter("step", stepId)
                .setParameter("agent", agentId)
                .uniqueResult()
                ?.let(StepState::of)
        }

    private fun update(
        tenantId: UUID,
        stepId: UUID,
        report: ProgressReport,
    ): Boolean =
        sessions.inTenant(tenantId) { session ->
            session
                .createNativeMutationQuery(UPDATE)
                .setParameter("phase", report.phase, String::class.java)
                .setParameter("processed", report.bytesProcessed, Long::class.javaObjectType)
                .setParameter("total", report.bytesTotal, Long::class.javaObjectType)
                .setParameter("filesProcessed", report.filesProcessed, Long::class.javaObjectType)
                .setParameter("filesTotal", report.filesTotal, Long::class.javaObjectType)
                .setParameter("tenant", tenantId)
                .setParameter("step", stepId)
                .executeUpdate() == 1
        }

    private companion object {
        const val ACCEPTED = "accepted"
    }
}
