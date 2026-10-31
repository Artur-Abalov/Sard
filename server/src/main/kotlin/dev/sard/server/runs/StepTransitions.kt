// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.RunStepRecord
import dev.sard.server.persistence.TenantSessions
import org.hibernate.Session
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID

/** The message of a step the server timed out: the agent sent no result within the timeout and the lost window. */
internal const val SILENT_MESSAGE = "no result from the agent within the step timeout of "

private fun silentMessage(timeoutSeconds: Long) = SILENT_MESSAGE + timeoutText(timeoutSeconds)

/** The message of a step the agent lost (ADR 0038; REST shows it in RunStep.message). */
const val LOST_MESSAGE = "agent lost the step"

private const val ACTIVE =
    "select s, r.cancelRequestedAt from RunStepRecord s join RunRecord r on r.id = s.runId " +
        "where s.agentId = :agent and s.status in ('queued', 'dispatched', 'running') order by s.queuedAt, s.id"

// Native SQL names tenant_id explicitly (ADR 0013, rule 8); every WHERE names the status it expects.
// A step the agent touches, or that is sent, has no lost deadline (FXs): only LOSE leaves it set.
internal const val STEP = "where tenant_id = :tenant and id = :step"
internal const val NO_DEADLINE = "lost_deadline = null"
private const val CANCEL_MARKED =
    "exists (select 1 from runs r where r.tenant_id = run_steps.tenant_id and r.id = run_steps.run_id " +
        "and r.cancel_requested_at is not null)"
private const val CLAIM =
    "update run_steps set status = 'dispatched', dispatched_at = :now, $NO_DEADLINE $STEP and status = 'queued'"
private const val SET_QUEUED = "set status = 'queued', dispatched_at = null, $NO_DEADLINE"
private const val RELEASE = "update run_steps $SET_QUEUED $STEP and status = 'dispatched' and not $CANCEL_MARKED"
private const val REDISPATCH =
    "update run_steps set dispatched_at = :now, $NO_DEADLINE $STEP and status = 'dispatched' " +
        "and dispatched_at < :before"
private const val ACCEPT =
    "update run_steps set status = 'running', phase = :phase, started_at = :now, $NO_DEADLINE " +
        "$STEP and status = 'dispatched'"
internal const val MESSAGE = "message = cast(:message as text), finished_at = :now"
private const val CLOSE = "update run_steps set status = :status, $MESSAGE $STEP"
private const val AGENT_TIMED_OUT = "timed_out_by = case when cast(:status as text) = 'timed_out' then 'agent' end"
private const val FINISH =
    "update run_steps set status = :status, $MESSAGE, output = cast(:output as jsonb), $AGENT_TIMED_OUT, " +
        "$NO_DEADLINE $STEP and status in ('dispatched', 'running')"

private const val CANCEL_UNSENT = "$CLOSE and status = 'dispatched' and $CANCEL_MARKED"

// The server closes a running step its agent confirmed and has sent no result past the timeout and the window (J1b).
private const val TIMEOUT_OF = "select timeout_seconds from run_steps $STEP"
private const val TIME_OUT =
    "update run_steps set status = 'timed_out', timed_out_by = 'server', $MESSAGE $STEP and $SILENT_STEP"
private const val DUE = "status in ('dispatched', 'running') and lost_deadline <= :now"

// A step whose run is marked for cancel is not lost: its agent did not confirm the cancel (J1a, Р-J4). Exactly one of
// the two moves it, whichever way a cancel and the deadline race.
private const val LOSE = "$CLOSE and $DUE and not $CANCEL_MARKED"
private const val UNCONFIRM = "$CLOSE and $DUE and $CANCEL_MARKED"

private const val OF_STEP = "where tenant_id = :tenant and id = (select run_id from run_steps $STEP)"
private const val RUN_STATUS = "update runs set status = :run $OF_STEP"
private const val RUN_STARTED = "update runs set status = :run, started_at = :now $OF_STEP"

// A run with a cancel mark records how the cancel ended: by the step's final status, or as the caller says.
internal const val RUN_FINISHED =
    "update runs set status = :run, $MESSAGE, " +
        "cancel_result = case when cancel_requested_at is null then null else cast(:cancelResult as text) end $OF_STEP"

/** How a cancel ended when [step] closed the run: the agent's confirmation, or something that came first. */
internal fun cancelResultOf(step: StepState): String =
    (if (step == StepState.CANCELLED) CancelResult.CANCELLED else CancelResult.FINISHED_FIRST).stored

/** The timeout of a step in seconds, null when it has none or is not found. */
private fun Session.timeoutOf(
    tenantId: UUID,
    stepId: UUID,
): Long? =
    createNativeQuery(TIMEOUT_OF, Long::class.javaObjectType)
        .setParameter("tenant", tenantId)
        .setParameter("step", stepId)
        .uniqueResult()

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

    /**
     * running → timed_out by the server: the agent confirmed the step, and has sent no result past its timeout and
     * the lost [window] (J1b, Р-J7). False when the step is not (or not yet) in that state.
     */
    fun timedOutBySilence(
        tenantId: UUID,
        stepId: UUID,
        window: Duration,
    ): Boolean

    /** dispatched or running → lost: its lost deadline has come and the agent did not report it (FXs). */
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
    private val events: RunEvents,
) : DispatchLedger {
    override fun active(
        tenantId: UUID,
        agentId: UUID,
    ): List<StepView> =
        sessions.inTenant(tenantId) { session ->
            session
                .createSelectionQuery(ACTIVE, Array<Any?>::class.java)
                .setParameter("agent", agentId)
                .list()
                .map { RunViews.step(it[0] as RunStepRecord).copy(cancelRequested = it[1] != null) }
        }

    override fun claim(
        tenantId: UUID,
        stepId: UUID,
    ) = move(tenantId, stepId, StepState.DISPATCHED, CLAIM, RUN_STATUS)

    /** dispatched → queued; a step whose run was marked for cancel meanwhile is cancelled instead, never sent. */
    override fun release(
        tenantId: UUID,
        stepId: UUID,
    ): Boolean {
        if (move(tenantId, stepId, StepState.QUEUED, RELEASE, RUN_STATUS)) return true
        val values = mapOf("message" to CANCELLED_BEFORE_DISPATCH, "cancelResult" to CancelResult.CANCELLED.stored)
        val cancelled = move(tenantId, stepId, StepState.CANCELLED, CANCEL_UNSENT, RUN_FINISHED, values)
        if (cancelled) announcer.announce(tenantId, stepId)
        return cancelled
    }

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
        // The agent of a step the person cancelled did not come back: cancelled, the agent did not confirm. The cancel
        // may be marked between the two attempts, so the last one asks again and the tick need not repeat.
        val values = mapOf("message" to CANCEL_UNCONFIRMED_MESSAGE, "cancelResult" to CancelResult.UNCONFIRMED.stored)
        val unconfirmed = { move(tenantId, stepId, StepState.CANCELLED, UNCONFIRM, RUN_FINISHED, values) }
        val lose = { move(tenantId, stepId, StepState.LOST, LOSE, RUN_FINISHED, mapOf("message" to LOST_MESSAGE)) }
        val moved = unconfirmed() || lose() || unconfirmed()
        if (moved) announcer.announce(tenantId, stepId)
        return moved
    }

    override fun timedOutBySilence(
        tenantId: UUID,
        stepId: UUID,
        window: Duration,
    ): Boolean {
        val moved =
            sessions.inTenant(tenantId) { session ->
                val timeout = session.timeoutOf(tenantId, stepId) ?: return@inTenant false
                val values = mapOf("message" to silentMessage(timeout), "window" to window.seconds)
                session.move(tenantId, stepId, StepState.TIMED_OUT, TIME_OUT, RUN_FINISHED, values)
            }
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
                "cancelResult" to cancelResultOf(to),
            ) + extra
        val moved = execute(step, values) == 1
        if (moved && run != null) events.movingStep(this, tenantId, stepId) { execute(run, values) }
        return moved
    }
}
