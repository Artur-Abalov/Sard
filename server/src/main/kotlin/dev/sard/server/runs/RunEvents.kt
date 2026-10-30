// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.events.ActorSource
import dev.sard.server.events.SardEventPublisher
import dev.sard.server.extension.RunCreated
import dev.sard.server.extension.RunStarted
import dev.sard.server.extension.RunStatusChanged
import dev.sard.server.extension.SardEvent
import dev.sard.server.extension.Subject
import dev.sard.server.persistence.RunRecord
import jakarta.transaction.Status
import jakarta.transaction.Synchronization
import org.hibernate.Session
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.time.Clock
import java.util.UUID

private const val RUN_STATUS_OF_STEP =
    "select r.id, r.status from runs r where r.tenant_id = :tenant and " +
        "r.id = (select run_id from run_steps where tenant_id = :tenant and id = :step)"
private const val RUN_STATUS = "select status from runs where tenant_id = :tenant and id = :run"
private const val VERIFICATION = "verification"

/**
 * The events of a run (CORE1 Р19): created, started and every change of the field status. A caller asks for a
 * [Watch] before it moves the run in its transaction and calls [settled] after: what the database then shows
 * decides, so no caller names the status it moved to. The events are published when the transaction has
 * committed, in the order they happened, and not at all if it rolls back.
 */
class RunEvents(
    private val publisher: SardEventPublisher,
    private val clock: Clock,
    private val actors: ActorSource,
) {
    /** The status a run had when a transaction began to move it. */
    class Watch(
        val tenantId: UUID,
        val runId: UUID,
        val from: String,
    )

    /** [run] was persisted in [session]'s transaction (status queued). */
    fun created(
        session: Session,
        run: RunRecord,
    ) {
        val at = clock.instant()
        val actor = actorOf(run) ?: actors.current()
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
            ?.let { Watch(tenantId, it[0] as UUID, it[1] as String) }

    /** The run [runId] as it is now. */
    fun watchRun(
        session: Session,
        tenantId: UUID,
        runId: UUID,
    ): Watch? = statusOf(session, tenantId, runId)?.let { Watch(tenantId, runId, it) }

    /** [watches] have been moved in [session]'s transaction: publishes what changed once it commits. */
    fun settled(
        session: Session,
        watches: List<Watch>,
    ) {
        for (watch in watches) {
            val to = statusOf(session, watch.tenantId, watch.runId)
            if (to != null && to != watch.from) record(session, watch, to)
        }
    }

    private fun record(
        session: Session,
        watch: Watch,
        to: String,
    ) {
        val run = session.find(RunRecord::class.java, watch.runId) ?: return
        val at = clock.instant()
        val actor = actorOf(run)
        val terminal = !RunState.of(to).active
        val kind = kindOf(run)
        val tenant = watch.tenantId
        val changed =
            RunStatusChanged(at, tenant, actor, run.id, kind, run.trigger, run.sourceId, watch.from, to, terminal)
        val started = RunStarted(at, tenant, actor, run.id, kind, run.trigger, run.sourceId)
        val events: List<SardEvent> = if (to == RunState.RUNNING.stored) listOf(changed, started) else listOf(changed)
        afterCommit(session) { events.forEach(publisher::publish) }
    }

    private fun statusOf(
        session: Session,
        tenantId: UUID,
        runId: UUID,
    ): String? =
        session
            .createNativeQuery(RUN_STATUS, String::class.java)
            .setParameter("tenant", tenantId)
            .setParameter("run", runId)
            .resultList
            .firstOrNull()

    /** A manual run was asked for by the administrator; a scheduled one by nobody. */
    private fun actorOf(run: RunRecord): Subject? = run.requestedBy?.let { Subject.ADMINISTRATOR }

    private fun kindOf(run: RunRecord) = if (run.trigger == Trigger.VERIFICATION.stored) VERIFICATION else run.kind

    private fun afterCommit(
        session: Session,
        action: () -> Unit,
    ) {
        session.transaction.registerSynchronization(
            object : Synchronization {
                override fun beforeCompletion() = Unit

                override fun afterCompletion(status: Int) {
                    if (status == Status.STATUS_COMMITTED) action()
                }
            },
        )
    }

    companion object {
        /** Tells nobody; for the code that is built without a context. */
        val NONE = RunEvents(SardEventPublisher.NONE, Clock.systemUTC(), ActorSource.NONE)
    }
}

/** The bean of [RunEvents], which the places that move a run share. */
@Configuration(proxyBeanMethods = false)
class RunEventsConfiguration {
    @Bean
    fun runEvents(
        publisher: SardEventPublisher,
        clock: Clock,
        actors: ActorSource,
    ) = RunEvents(publisher, clock, actors)
}
