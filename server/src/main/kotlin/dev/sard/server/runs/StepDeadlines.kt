// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.TenantSessions
import org.hibernate.Session
import java.time.Duration
import java.time.Instant
import java.util.UUID

private const val IN_FLIGHT = "status in ('dispatched', 'running')"
private const val OVERDUE = "select tenant_id, id from run_steps where $IN_FLIGHT and lost_deadline <= :now"

// A running step the agent confirmed (no lost deadline) that has a timeout, past the timeout and the window (J1b).
internal const val SILENT_STEP =
    "status = 'running' and lost_deadline is null and timeout_seconds is not null " +
        "and started_at + (timeout_seconds + :window) * interval '1 second' <= :now"
private const val SILENT = "select tenant_id, id, agent_id from run_steps where $SILENT_STEP"

// A queued step whose run's wait for an agent is over, with its agent's last contact (J1c).
private const val WAITS_OVER =
    "select s.tenant_id, s.id, s.agent_id, a.last_seen_at from run_steps s " +
        "join runs r on r.tenant_id = s.tenant_id and r.id = s.run_id " +
        "join agents a on a.tenant_id = s.tenant_id and a.id = s.agent_id " +
        "where r.status = 'queued' and s.status = 'queued' and r.agent_wait_until <= :now"
private const val COL_TENANT = 0
private const val COL_STEP = 1
private const val COL_AGENT = 2
private const val COL_SEEN = 3
private const val HOLDERS = "select distinct tenant_id, agent_id from run_steps where $IN_FLIGHT"

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). least() skips NULL: the earliest deadline holds (Д2).
private const val OF_AGENT = "where tenant_id = :tenant and agent_id = :agent and $IN_FLIGHT"
private const val LISTED = "and id in (:steps)"
private const val EXPECT = "update run_steps set lost_deadline = least(lost_deadline, :deadline) $OF_AGENT"
private const val CONFIRM = "update run_steps set lost_deadline = null $OF_AGENT $LISTED"
private const val RESTART = "update run_steps set lost_deadline = :deadline $OF_AGENT"

/** A running step whose agent has stayed silent past the step's timeout and the lost window (J1b). */
data class SilentStep(
    val tenantId: UUID,
    val id: UUID,
    val agentId: UUID,
)

/** A queued step whose run's wait for its agent is over, with the agent's last contact (J1c). */
data class WaitOver(
    val tenantId: UUID,
    val stepId: UUID,
    val agentId: UUID,
    val lastSeenAt: Instant?,
)

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

    /**
     * Running steps whose timeout and the lost [window] have passed at [now]: the agent confirmed them (they have no
     * lost deadline, which is the loss window's business) and sent no result (Р-J7). Across tenants.
     */
    fun silent(
        now: Instant,
        window: Duration,
    ): List<SilentStep>

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
    override fun overdue(now: Instant) = system(OVERDUE, mapOf("now" to now), ::inTenant)

    /** Queued steps of runs whose wait for an agent has ended at [now], across tenants (J1c, ids and times only). */
    fun waitsOver(now: Instant): List<WaitOver> = system(WAITS_OVER, mapOf("now" to now), ::waitOver)

    override fun holders() = system(HOLDERS, emptyMap(), ::inTenant)

    override fun silent(
        now: Instant,
        window: Duration,
    ): List<SilentStep> = system(SILENT, mapOf("now" to now, "window" to window.seconds), ::silentStep)

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

    private fun <T> system(
        sql: String,
        values: Map<String, Any>,
        row: (Array<Any?>) -> T,
    ): List<T> =
        sessions.system { session ->
            val query = session.createNativeQuery(sql, Array<Any?>::class.java)
            values.forEach { (name, value) -> query.setParameter(name, value) }
            query.list().map(row)
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

private fun Array<Any?>.uuid(column: Int) = this[column] as UUID

private fun inTenant(row: Array<Any?>) = InTenant(row.uuid(COL_TENANT), row.uuid(COL_STEP))

private fun silentStep(row: Array<Any?>) = SilentStep(row.uuid(COL_TENANT), row.uuid(COL_STEP), row.uuid(COL_AGENT))

private fun waitOver(row: Array<Any?>): WaitOver {
    val seen = row[COL_SEEN] as Instant?
    return WaitOver(row.uuid(COL_TENANT), row.uuid(COL_STEP), row.uuid(COL_AGENT), seen)
}
