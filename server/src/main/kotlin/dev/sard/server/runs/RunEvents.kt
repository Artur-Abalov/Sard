// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.events.AfterCommit
import dev.sard.server.events.SardEventPublisher
import dev.sard.server.events.SubjectActors
import dev.sard.server.extension.RunCreated
import dev.sard.server.extension.RunStarted
import dev.sard.server.extension.RunStatusChanged
import dev.sard.server.extension.SardEvent
import dev.sard.server.extension.Subject
import dev.sard.server.persistence.RunRecord
import org.hibernate.Session
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.time.Clock
import java.util.UUID

private const val RUN_STATUS_OF_STEP =
    "select r.id, r.status, r.started_at is not null from runs r where r.tenant_id = :tenant and " +
        "r.id = (select run_id from run_steps where tenant_id = :tenant and id = :step)"
private const val RUN_STATE = "select status, started_at is not null from runs where tenant_id = :tenant and id = :run"
private const val RUN_REQUESTER = "select requested_by_kind, requested_by_id from runs where id = :run"
private const val SET_REQUESTER =
    "update runs set requested_by_kind = :kind, requested_by_id = :id where id = :run and requested_by is not null"
private const val VERIFICATION = "verification"

/**
 * The events of a run (CORE1 Р19): created, started (when the run gets its `started_at`, once, Р29) and every change
 * of the field status. A caller asks for a [Watch] before it moves the run in its transaction and calls [settled]
 * after: what the database then shows decides, so no caller names the status it moved to. The events are published
 * when the transaction has committed, in the order they happened, and not at all if it rolls back.
 */
class RunEvents(
    private val publisher: SardEventPublisher,
    private val clock: Clock,
    private val actors: SubjectActors,
) {
    /** The status a run had, and whether it had its start moment, when a transaction began to move it. */
    class Watch(
        val tenantId: UUID,
        val runId: UUID,
        val from: String,
        val started: Boolean = false,
    )

    /** What the database shows of a run: its status and whether `started_at` is set. */
    private class Snapshot(
        val status: String,
        val started: Boolean,
    )

    /** [run] was persisted in [session]'s transaction (status queued). */
    fun created(
        session: Session,
        run: RunRecord,
    ) {
        val at = clock.instant()
        val actor = actors.current()
        if (actor != null) Requester.remember(session, run, actor)
        afterCommit(session) {
            val tenantId = checkNotNull(run.tenantId)
            publisher.publish(RunCreated(at, tenantId, actor, run.id, kindOf(run), run.trigger, run.sourceId))
        }
    }

    /** The run of [stepId] as it is now; null when there is none. */
    fun watchStep(
        session: Session,
        tenantId: UUID,
        stepId: UUID,
    ): Watch? =
        session
            .createNativeQuery(RUN_STATUS_OF_STEP, Array<Any?>::class.java)
            .setParameter("tenant", tenantId)
            .setParameter("step", stepId)
            .resultList
            .firstOrNull()
            ?.let { Watch(tenantId, it[0] as UUID, it[1] as String, it[2] as Boolean) }

    /** The run [runId] as it is now. */
    fun watchRun(
        session: Session,
        tenantId: UUID,
        runId: UUID,
    ): Watch? = snapshotOf(session, tenantId, runId)?.let { Watch(tenantId, runId, it.status, it.started) }

    /** Runs [move], which moves the run of [stepId], between [watchStep] and [settled]; what [move] returns. */
    fun <T> movingStep(
        session: Session,
        tenantId: UUID,
        stepId: UUID,
        move: () -> T,
    ): T {
        val watch = watchStep(session, tenantId, stepId)
        return move().also { settled(session, listOfNotNull(watch)) }
    }

    /** Runs [move], which moves the run [runId], between [watchRun] and [settled]; what [move] returns. */
    fun <T> movingRun(
        session: Session,
        tenantId: UUID,
        runId: UUID,
        move: () -> T,
    ): T {
        val watch = watchRun(session, tenantId, runId)
        return move().also { settled(session, listOfNotNull(watch)) }
    }

    /** [watches] have been moved in [session]'s transaction: publishes what changed once it commits. */
    fun settled(
        session: Session,
        watches: List<Watch>,
    ) {
        for (watch in watches) {
            snapshotOf(session, watch.tenantId, watch.runId)?.let { record(session, watch, it) }
        }
    }

    /** The status change first, then the start, for what differs from [watch]; nothing when nothing does. */
    private fun record(
        session: Session,
        watch: Watch,
        now: Snapshot,
    ) {
        val change = Change(statusChanged = now.status != watch.from, startedNow = now.started && !watch.started)
        if (!change.happened) return
        val run = session.find(RunRecord::class.java, watch.runId) ?: return
        val events = eventsOf(session, run, watch, now, change)
        afterCommit(session) { events.forEach(publisher::publish) }
    }

    /** What moved between a [Watch] and the run's state now. */
    private class Change(
        val statusChanged: Boolean,
        val startedNow: Boolean,
    ) {
        val happened get() = statusChanged || startedNow
    }

    /** The change of status, then the start, of [run] between [watch] and [now], each only if it happened. */
    private fun eventsOf(
        session: Session,
        run: RunRecord,
        watch: Watch,
        now: Snapshot,
        change: Change,
    ): List<SardEvent> {
        val at = clock.instant()
        val actor = Requester.requesterOf(session, run)
        val kind = kindOf(run)
        val tenant = watch.tenantId
        val terminal = !RunState.of(now.status).active
        val changed =
            RunStatusChanged(
                at,
                tenant,
                actor,
                run.id,
                kind,
                run.trigger,
                run.sourceId,
                watch.from,
                now.status,
                terminal,
            )
        val started = RunStarted(at, tenant, actor, run.id, kind, run.trigger, run.sourceId)
        return listOfNotNull(changed.takeIf { change.statusChanged }, started.takeIf { change.startedNow })
    }

    private fun snapshotOf(
        session: Session,
        tenantId: UUID,
        runId: UUID,
    ): Snapshot? =
        session
            .createNativeQuery(RUN_STATE, Array<Any?>::class.java)
            .setParameter("tenant", tenantId)
            .setParameter("run", runId)
            .resultList
            .firstOrNull()
            ?.let { Snapshot(it[0] as String, it[1] as Boolean) }

    private fun kindOf(run: RunRecord) = if (run.trigger == Trigger.VERIFICATION.stored) VERIFICATION else run.kind

    private fun afterCommit(
        session: Session,
        action: () -> Unit,
    ) {
        session.transaction.registerSynchronization(AfterCommit(action))
    }

    companion object {
        /** Tells nobody; for the code that is built without a context. */
        val NONE = RunEvents(SardEventPublisher.NONE, Clock.systemUTC(), SubjectActors.NONE)
    }
}

/** The subject behind a manual run, which the agent's thread cannot name later (R26). */
private object Requester {
    /** A manual run keeps the subject that asked for it (the agent's thread names nobody); a scheduled one has none. */
    fun remember(
        session: Session,
        run: RunRecord,
        actor: Subject,
    ) {
        session
            .createNativeQuery(SET_REQUESTER)
            .setParameter("kind", actor.kind)
            .setParameter("id", actor.id)
            .setParameter("run", run.id)
            .executeUpdate()
    }

    fun requesterOf(
        session: Session,
        run: RunRecord,
    ): Subject? =
        session
            .createNativeQuery(RUN_REQUESTER, Array<Any?>::class.java)
            .setParameter("run", run.id)
            .resultList
            .firstOrNull()
            ?.let { row -> (row[0] as String?)?.let { Subject(it, row[1] as String) } }
}

/** The bean of [RunEvents], which the places that move a run share. */
@Configuration(proxyBeanMethods = false)
class RunEventsConfiguration {
    @Bean
    fun runEvents(
        publisher: SardEventPublisher,
        clock: Clock,
        actors: SubjectActors,
    ) = RunEvents(publisher, clock, actors)
}
