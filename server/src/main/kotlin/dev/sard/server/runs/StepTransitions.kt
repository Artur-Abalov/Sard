// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.RunStepRecord
import dev.sard.server.persistence.TenantSessions
import org.hibernate.Session
import java.time.Clock
import java.time.Instant
import java.util.UUID

/** The message of a step the agent lost (S6a ADR draft; REST shows it in RunStep.message). */
const val LOST_MESSAGE = "agent lost the step"

private const val ACTIVE =
    "from RunStepRecord where agentId = :agent and status in ('queued', 'dispatched', 'running') order by queuedAt, id"

// Native SQL names tenant_id explicitly (ADR 0013, rule 8); every WHERE names the status it expects.
private const val STEP = "where tenant_id = :tenant and id = :step"
private const val CLAIM = "update run_steps set status = 'dispatched', dispatched_at = :now $STEP and status = 'queued'"
private const val SET_QUEUED = "set status = 'queued', dispatched_at = null"
private const val RELEASE = "update run_steps $SET_QUEUED $STEP and status = 'dispatched'"
private const val REDISPATCH =
    "update run_steps set dispatched_at = :now $STEP and status = 'dispatched' and dispatched_at < :before"
private const val ACCEPT =
    "update run_steps set status = 'running', phase = :phase, started_at = :now $STEP and status = 'dispatched'"
private const val MESSAGE = "message = cast(:message as text), finished_at = :now"
private const val CLOSE = "update run_steps set status = :status, $MESSAGE $STEP"
private const val FINISH =
    "update run_steps set status = :status, $MESSAGE, output = cast(:output as jsonb) $STEP " +
        "and status in ('dispatched', 'running')"
private const val LOSE = "$CLOSE and status = 'running'"

private const val OF_STEP = "where tenant_id = :tenant and id = (select run_id from run_steps $STEP)"
private const val RUN_STATUS = "update runs set status = :run $OF_STEP"
private const val RUN_STARTED = "update runs set status = :run, started_at = :now $OF_STEP"
private const val RUN_FINISHED = "update runs set status = :run, $MESSAGE $OF_STEP"

/** How the agent says a step ended (S7 maps StepResult to it). */
data class StepOutcome(
    val status: StepState,
    /** The agent's message; null or empty on success. */
    val message: String?,
) {
    init {
        require(!status.active) { "$status is not a final state" }
    }
}

/** What the dispatcher (agents/dispatch) reads and moves; [StepTransitions] is the implementation. */
interface DispatchLedger {
    /** The agent's queued, dispatched and running steps, in creation order. */
    fun active(
        tenantId: UUID,
        agentId: UUID,
    ): List<StepView>

    /** queued → dispatched, before sending: whoever moves the row sends it. */
    fun claim(
        tenantId: UUID,
        stepId: UUID,
    ): Boolean

    /** dispatched → queued: the session refused the command. */
    fun release(
        tenantId: UUID,
        stepId: UUID,
    ): Boolean

    /** dispatched, sent before [sentBefore] → dispatched now: sent again after a Hello that did not list it. */
    fun redispatch(
        tenantId: UUID,
        stepId: UUID,
        sentBefore: Instant,
    ): Boolean

    /** running → lost: the agent did not list it in Hello and no result came within the window. */
    fun lost(
        tenantId: UUID,
        stepId: UUID,
    ): Boolean
}

/**
 * Every status change of a step after it is queued, each one a guarded update in its own
 * transaction: the step row only moves from the status the caller expects (zero rows: another
 * path moved it first, and its state stays), and the run follows it in the same transaction.
 * S7 calls [accepted] and, through [StepResults], [finish]; the dispatcher calls the rest. A move
 * that finishes the run publishes [RunFinished] after its commit.
 */
class StepTransitions(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val announcer: RunAnnouncer = RunAnnouncer(sessions, RunFinishedPublisher.NONE),
) : DispatchLedger {
    override fun active(
        tenantId: UUID,
        agentId: UUID,
    ): List<StepView> =
        sessions.inTenant(tenantId) { session ->
            session
                .createSelectionQuery(ACTIVE, RunStepRecord::class.java)
                .setParameter("agent", agentId)
                .list()
                .map { RunViews.step(it) }
        }

    override fun claim(
        tenantId: UUID,
        stepId: UUID,
    ) = move(tenantId, stepId, StepState.DISPATCHED, CLAIM, RUN_STATUS)

    override fun release(
        tenantId: UUID,
        stepId: UUID,
    ) = move(tenantId, stepId, StepState.QUEUED, RELEASE, RUN_STATUS)

    override fun redispatch(
        tenantId: UUID,
        stepId: UUID,
        sentBefore: Instant,
    ) = move(tenantId, stepId, StepState.DISPATCHED, REDISPATCH, run = null, extra = mapOf("before" to sentBefore))

    /** dispatched → running with [phase] (S7: the agent sent ACCEPTED). */
    fun accepted(
        tenantId: UUID,
        stepId: UUID,
        phase: String,
    ) = move(tenantId, stepId, StepState.RUNNING, ACCEPT, RUN_STARTED, mapOf("phase" to phase))

    /** dispatched or running → [outcome] (a StepResult without output); false for a step already closed. */
    fun finished(
        tenantId: UUID,
        stepId: UUID,
        outcome: StepOutcome,
    ): Boolean {
        val moved = sessions.inTenant(tenantId) { finish(it, tenantId, stepId, outcome, output = null) }
        if (moved) announcer.announce(tenantId, stepId)
        return moved
    }

    /**
     * dispatched or running → [outcome] with [output] (JSON) inside the caller's transaction, so
     * that S7 records the rest of the result with it; the caller announces the run after commit.
     */
    fun finish(
        session: Session,
        tenantId: UUID,
        stepId: UUID,
        outcome: StepOutcome,
        output: String?,
    ): Boolean {
        val values = mapOf("message" to outcome.message?.ifEmpty { null }, "output" to output)
        return session.move(tenantId, stepId, outcome.status, FINISH, RUN_FINISHED, values)
    }

    override fun lost(
        tenantId: UUID,
        stepId: UUID,
    ): Boolean {
        val moved = move(tenantId, stepId, StepState.LOST, LOSE, RUN_FINISHED, mapOf("message" to LOST_MESSAGE))
        if (moved) announcer.announce(tenantId, stepId)
        return moved
    }

    private fun move(
        tenantId: UUID,
        stepId: UUID,
        to: StepState,
        step: String,
        run: String?,
        extra: Map<String, Any?> = emptyMap(),
    ): Boolean = sessions.inTenant(tenantId) { it.move(tenantId, stepId, to, step, run, extra) }

    private fun Session.move(
        tenantId: UUID,
        stepId: UUID,
        to: StepState,
        step: String,
        run: String?,
        extra: Map<String, Any?>,
    ): Boolean {
        val values =
            mapOf(
                "tenant" to tenantId,
                "step" to stepId,
                "now" to clock.instant(),
                "status" to to.stored,
                "run" to RunState.following(to).stored,
            ) + extra
        val moved = execute(step, values) == 1
        if (moved && run != null) execute(run, values)
        return moved
    }

    /** Binds the [values] that [sql] names; text values are typed so that NULL binds as text. */
    private fun Session.execute(
        sql: String,
        values: Map<String, Any?>,
    ): Int {
        val query = createNativeMutationQuery(sql)
        for ((name, value) in values.filterKeys { ":$it" in sql }) {
            if (name in TEXT_VALUES) {
                query.setParameter(name, value as String?, String::class.java)
            } else {
                query.setParameter(name, value)
            }
        }
        return query.executeUpdate()
    }
}

private val TEXT_VALUES = setOf("message", "output")
