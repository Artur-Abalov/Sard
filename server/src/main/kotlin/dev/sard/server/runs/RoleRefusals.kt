// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.role.AgentRole
import java.time.Clock
import java.util.UUID

// A step that never left the queue still gets a dispatch moment: run_steps_dispatched_check ties it to the status.
private const val REFUSE =
    "update run_steps set status = :status, $MESSAGE, dispatched_at = coalesce(dispatched_at, :now), $NO_DEADLINE " +
        "$STEP and status in ('queued', 'dispatched')"

/** What the dispatcher asks about roles (V1): who may be sent what, and the end of a step that may not be sent. */
interface RoleLedger {
    /** The role of the agent: what the dispatcher may send it. */
    fun roleOf(
        tenantId: UUID,
        agentId: UUID,
    ): AgentRole

    /** queued or dispatched → failed with [message]: the agent's role does not allow the step's action. */
    fun refuse(
        tenantId: UUID,
        stepId: UUID,
        message: String,
    ): Boolean
}

/** [RoleLedger] over the database; the step and its run end in one transaction, then the run is announced. */
class RoleRefusals(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val announcer: RunAnnouncer,
) : RoleLedger {
    override fun roleOf(
        tenantId: UUID,
        agentId: UUID,
    ): AgentRole =
        sessions.inTenant(tenantId) { session ->
            AgentRole.of(checkNotNull(session.find(Agent::class.java, agentId)) { "no agent $agentId" }.role)
        }

    override fun refuse(
        tenantId: UUID,
        stepId: UUID,
        message: String,
    ): Boolean {
        val values =
            mapOf(
                "tenant" to tenantId,
                "step" to stepId,
                "now" to clock.instant(),
                "status" to StepState.FAILED.stored,
                "run" to RunState.FAILED.stored,
                "message" to message,
                "cancelResult" to cancelResultOf(StepState.FAILED),
            )
        val moved =
            sessions.inTenant(tenantId) { session ->
                val ended = session.execute(REFUSE, values) == 1
                if (ended) {
                    val watch = announcer.events.watchStep(session, tenantId, stepId)
                    session.execute(RUN_FINISHED, values)
                    announcer.events.settled(session, listOfNotNull(watch))
                }
                ended
            }
        if (moved) announcer.announce(tenantId, stepId)
        return moved
    }
}
