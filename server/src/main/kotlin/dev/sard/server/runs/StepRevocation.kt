// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import org.hibernate.Session
import java.time.Instant
import java.util.UUID

/** The message of a step whose agent was revoked while it was waiting or running (S8b В5). */
const val REVOKED_MESSAGE = "agent revoked"

private const val ACTIVE_STATUSES = "status in ('queued', 'dispatched', 'running')"
private const val REVOKE_STEPS =
    "update run_steps set status = 'lost', message = '$REVOKED_MESSAGE', finished_at = :now, " +
        "dispatched_at = coalesce(dispatched_at, :now) " +
        "where tenant_id = :tenant and agent_id = :agent and $ACTIVE_STATUSES returning id, run_id"
private const val REVOKE_RUNS =
    "update runs set status = 'failed', message = '$REVOKED_MESSAGE', finished_at = :now " +
        "where tenant_id = :tenant and id in (:runs) and $ACTIVE_STATUSES"

/** What revoking an agent does to its steps. */
object StepRevocation {
    /**
     * The agent was revoked (S8b В5): its queued, dispatched and running steps are lost now, with
     * [REVOKED_MESSAGE], and their runs fail with it, inside the caller's transaction (the one that
     * revokes the agent). No window will ever do it: a revoked agent never sends Hello. Returns the
     * steps; the caller announces their runs after the commit.
     */
    fun loseSteps(
        session: Session,
        tenantId: UUID,
        agentId: UUID,
        now: Instant,
    ): List<UUID> {
        val lost =
            session
                .createNativeQuery(REVOKE_STEPS, Array<Any?>::class.java)
                .setParameter("tenant", tenantId)
                .setParameter("agent", agentId)
                .setParameter("now", now)
                .resultList
        if (lost.isEmpty()) return emptyList()
        session
            .createNativeMutationQuery(REVOKE_RUNS)
            .setParameter("tenant", tenantId)
            .setParameterList("runs", lost.map { it[1] as UUID })
            .setParameter("now", now)
            .executeUpdate()
        return lost.map { it[0] as UUID }
    }
}
