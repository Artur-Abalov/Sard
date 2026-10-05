// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.TenantSessions
import org.hibernate.Session
import java.time.Instant
import java.util.UUID

private const val IN_FLIGHT = "status in ('dispatched', 'running')"
private const val OVERDUE = "select tenant_id, id from run_steps where $IN_FLIGHT and lost_deadline <= :now"
private const val HOLDERS = "select distinct tenant_id, agent_id from run_steps where $IN_FLIGHT"

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). least() skips NULL: the earliest deadline holds (Д2).
private const val OF_AGENT = "where tenant_id = :tenant and agent_id = :agent and $IN_FLIGHT"
private const val LISTED = "and id in (:steps)"
private const val EXPECT = "update run_steps set lost_deadline = least(lost_deadline, :deadline) $OF_AGENT"
private const val CONFIRM = "update run_steps set lost_deadline = null $OF_AGENT $LISTED"
private const val RESTART = "update run_steps set lost_deadline = :deadline $OF_AGENT"

/** A step or an agent with the tenant it belongs to. */
data class InTenant(
    val tenantId: UUID,
    val id: UUID,
)

/**
 * The lost deadline of steps in flight (`run_steps.lost_deadline`, FXs): when a dispatched or
 * running step is lost unless its agent reports it. [StepTransitions.lost] makes the move; a send,
 * progress or a result clears the deadline in the same statement as its own transition.
 */
interface LostDeadlines {
    /** Dispatched and running steps whose lost deadline is [now] or earlier, across tenants. */
    fun overdue(now: Instant): List<InTenant>

    /** Agents with a dispatched or running step, across tenants. */
    fun holders(): List<InTenant>

    /** The agent's session ended: its steps in flight are lost at [deadline], or at the earlier one they have. */
    fun expectAll(
        tenantId: UUID,
        agentId: UUID,
        deadline: Instant,
    ): Int

    /** A Hello did not list [stepIds]: lost at [deadline], or at the earlier one they have. */
    fun expect(
        tenantId: UUID,
        agentId: UUID,
        stepIds: Collection<UUID>,
        deadline: Instant,
    ): Int

    /** A Hello listed [stepIds]: the agent has them, no deadline. */
    fun confirm(
        tenantId: UUID,
        agentId: UUID,
        stepIds: Collection<UUID>,
    ): Int

    /** The server started: the agent's steps in flight get [deadline], whatever they had. */
    fun restart(
        tenantId: UUID,
        agentId: UUID,
        deadline: Instant,
    ): Int
}

/** Reads across tenants through `system` (ADR 0013, list of callers: ids only); writes in the step's tenant. */
class StepDeadlines(
    private val sessions: TenantSessions,
) : LostDeadlines {
    override fun overdue(now: Instant) = read(OVERDUE, mapOf("now" to now))

    override fun holders() = read(HOLDERS, emptyMap())

    override fun expectAll(
        tenantId: UUID,
        agentId: UUID,
        deadline: Instant,
    ) = write(tenantId, agentId, EXPECT, mapOf("deadline" to deadline))

    override fun expect(
        tenantId: UUID,
        agentId: UUID,
        stepIds: Collection<UUID>,
        deadline: Instant,
    ) = listed(stepIds) { write(tenantId, agentId, "$EXPECT $LISTED", mapOf("steps" to it, "deadline" to deadline)) }

    override fun confirm(
        tenantId: UUID,
        agentId: UUID,
        stepIds: Collection<UUID>,
    ) = listed(stepIds) { write(tenantId, agentId, CONFIRM, mapOf("steps" to it)) }

    override fun restart(
        tenantId: UUID,
        agentId: UUID,
        deadline: Instant,
    ) = write(tenantId, agentId, RESTART, mapOf("deadline" to deadline))

    private fun read(
        sql: String,
        values: Map<String, Any>,
    ): List<InTenant> =
        sessions.system { session ->
            val query = session.createNativeQuery(sql, Array<Any>::class.java)
            values.forEach { (name, value) -> query.setParameter(name, value) }
            query.list().map { InTenant(it[0] as UUID, it[1] as UUID) }
        }

    private fun write(
        tenantId: UUID,
        agentId: UUID,
        sql: String,
        values: Map<String, Any>,
    ): Int = sessions.inTenant(tenantId) { it.update(sql, values + mapOf("tenant" to tenantId, "agent" to agentId)) }

    private fun Session.update(
        sql: String,
        values: Map<String, Any>,
    ): Int {
        val query = createNativeMutationQuery(sql)
        for ((name, value) in values) {
            if (value is Collection<*>) query.setParameterList(name, value) else query.setParameter(name, value)
        }
        return query.executeUpdate()
    }
}

/** `in ()` is not SQL: no ids, no statement. */
private fun listed(
    stepIds: Collection<UUID>,
    update: (Collection<UUID>) -> Int,
) = if (stepIds.isEmpty()) 0 else update(stepIds)
