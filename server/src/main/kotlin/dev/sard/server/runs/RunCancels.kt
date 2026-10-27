// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.RunRecord
import dev.sard.server.persistence.TenantSessions
import org.hibernate.Session
import java.time.Clock
import java.util.UUID

/** The message of a step cancelled while it was still in the queue: the agent never got it (J1a). */
const val CANCELLED_BEFORE_DISPATCH = "cancelled before dispatch"

/** The message of a step whose agent did not confirm its cancel within the lost window (J1a, Р-J4). */
const val CANCEL_UNCONFIRMED_MESSAGE = "the agent did not confirm the cancellation"

// Native SQL names tenant_id explicitly (ADR 0013, rule 8); every WHERE names the status it expects.
private const val OF_RUN = "where tenant_id = :tenant and id = :run"
private const val CANCEL_QUEUED_STEP =
    "update run_steps set status = 'cancelled', message = '$CANCELLED_BEFORE_DISPATCH', finished_at = :now " +
        "where tenant_id = :tenant and run_id = :run and status = 'queued'"
private const val CANCEL_QUEUED_RUN =
    "update runs set status = 'cancelled', message = '$CANCELLED_BEFORE_DISPATCH', finished_at = :now, " +
        "cancel_requested_at = :now, cancel_requested_by = :by, cancel_result = 'cancelled' $OF_RUN"
private const val MARK =
    "update runs set cancel_requested_at = :now, cancel_requested_by = :by, cancel_result = 'pending' " +
        "$OF_RUN and status in ('dispatched', 'running') and cancel_requested_at is null"
private const val STATE = "select status, cancel_requested_at is not null from runs $OF_RUN"

/** Tells the dispatcher that a run in flight was marked for cancel, once the mark is committed; must not throw. */
fun interface StepsCancelled {
    fun onCancelled(
        tenantId: UUID,
        agentId: UUID,
    )

    companion object {
        /** Nobody listens: the agent hears of the cancel at its next Hello. */
        val NONE = StepsCancelled { _, _ -> }
    }
}

/**
 * Cancels a run on a person's request (J1a): a queued run is closed at once, nothing goes to the agent; a run
 * in flight only gets the cancel mark and the dispatcher sends the agent its CancelStep. The mark is one guarded
 * update, so concurrent requests leave one record and a run that finished first answers 409 (`run_finished`).
 */
class RunCancels(
    private val sessions: TenantSessions,
    private val runs: Runs,
    private val clock: Clock,
    private val cancelled: StepsCancelled,
    private val announcer: RunAnnouncer,
) {
    /** What a request did to the run. */
    private enum class Done { CLOSED, MARKED, NOTHING }

    /** The run after the cancel; throws [RunNotFound], [RunNotCancellable] or [RunAlreadyFinished]. */
    fun cancel(
        tenantId: UUID,
        runId: UUID,
    ): RunView {
        val done = sessions.inTenant(tenantId) { attempt(it, tenantId, runId) }
        val view = runs.get(tenantId, runId) ?: throw RunNotFound(runId)
        when (done) {
            Done.CLOSED -> announcer.announce(tenantId, view.steps.single().id)
            Done.MARKED -> cancelled.onCancelled(tenantId, view.agentId)
            Done.NOTHING -> Unit
        }
        return view
    }

    /** Closed in the queue, marked in flight, or neither (already marked: nothing to do). */
    private fun attempt(
        session: Session,
        tenantId: UUID,
        runId: UUID,
    ): Done {
        val run = session.find(RunRecord::class.java, runId) ?: throw RunNotFound(runId)
        if (Trigger.of(run.trigger) == Trigger.VERIFICATION) throw RunNotCancellable(runId)
        val values = mapOf("tenant" to tenantId, "run" to runId, "now" to clock.instant(), "by" to ADMIN)
        return close(session, values) ?: mark(session, values, runId)
    }

    /** A queued run is closed with its step; null when the step is not queued (any more). */
    private fun close(
        session: Session,
        values: Map<String, Any?>,
    ): Done? {
        if (session.execute(CANCEL_QUEUED_STEP, values) != 1) return null
        session.execute(CANCEL_QUEUED_RUN, values)
        return Done.CLOSED
    }

    /** A run in flight gets the mark; one that has it already is left alone, a final one is refused. */
    private fun mark(
        session: Session,
        values: Map<String, Any?>,
        runId: UUID,
    ): Done {
        if (session.execute(MARK, values) == 1) return Done.MARKED
        requireActive(active(session, values), runId)
        return Done.NOTHING
    }

    /** Neither update moved the run: it is in flight with a mark already (true) or it is final. */
    private fun active(
        session: Session,
        values: Map<String, Any?>,
    ): Boolean {
        val query = session.createNativeQuery(STATE, Array<Any?>::class.java)
        query.setParameter("tenant", values["tenant"])
        query.setParameter("run", values["run"])
        return RunState.of(query.singleResult[0] as String).active
    }

    private fun requireActive(
        active: Boolean,
        runId: UUID,
    ) {
        if (!active) throw RunAlreadyFinished(runId)
    }
}
