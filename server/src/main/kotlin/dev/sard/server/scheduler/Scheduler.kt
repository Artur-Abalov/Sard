// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import dev.sard.server.persistence.ScheduleFireRecord
import dev.sard.server.persistence.ScheduleRecord
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.runs.AgentRevoked
import dev.sard.server.runs.InTenant
import dev.sard.server.runs.RunActive
import dev.sard.server.runs.RunView
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsException
import dev.sard.server.runs.SourceNotFound
import dev.sard.server.runs.StepsQueued
import dev.sard.server.runs.UnknownPlugin
import dev.sard.server.runs.UnknownRepository
import org.hibernate.Session
import org.slf4j.LoggerFactory
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.reflect.KClass

private val log = LoggerFactory.getLogger(Scheduler::class.java)

private const val IS_DUE = "(s.next_run_at <= :now or s.catch_up_at <= :now)"

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). A deleted source's schedule is never due.
private const val DUE =
    "select s.tenant_id, s.id from schedules s join sources src on src.tenant_id = s.tenant_id " +
        "and src.id = s.source_id where s.enabled and src.deleted_at is null and $IS_DUE " +
        "order by least(s.next_run_at, s.catch_up_at), s.id limit :batch"
private const val LAST_SLOT = "select max(catch_up_at) from schedules"
private const val LOCK =
    "select s.* from schedules s where s.tenant_id = :tenant and s.id = :id and s.enabled and $IS_DUE " +
        "for update skip locked"

/** A refused start of a schedule's run and what the journal says of it; [RunActive] is answered apart. */
private val REFUSALS: Map<KClass<out RunsException>, Pair<FireOutcome, FireReason>> =
    mapOf(
        SourceNotFound::class to (FireOutcome.SKIPPED_GONE to FireReason.SOURCE_DELETED),
        AgentRevoked::class to (FireOutcome.SKIPPED_GONE to FireReason.AGENT_REVOKED),
        UnknownPlugin::class to (FireOutcome.REFUSED to FireReason.UNKNOWN_PLUGIN),
        UnknownRepository::class to (FireOutcome.REFUSED to FireReason.UNKNOWN_REPOSITORY),
    )

/** `sard.scheduler.*` as the scheduler uses it. */
data class SchedulerSettings(
    /** Catch-ups owed after a downtime start this far apart (F3a answer 8). */
    val catchUpSpacing: Duration,
    /** The skip in a row that raises the alert, once per series (F3a answer 4). */
    val skipAlertThreshold: Int,
    /** Schedules handled per tick at most; the rest wait for the next one. */
    val batch: Int,
)

/**
 * Fires due schedules (F3a, D9, D16). Each tick reads the due schedules across tenants, then takes each
 * one's row with `FOR UPDATE SKIP LOCKED` in its tenant and, in that one transaction, records the fire,
 * creates its run through [Runs] and moves the schedule on. A second scheduler on the same database
 * skips a row being fired and finds a fired one no longer due. Nothing is kept in memory between ticks.
 */
class Scheduler(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val runs: Runs,
    private val ids: UuidV7,
    private val queued: StepsQueued,
    private val settings: SchedulerSettings,
) {
    fun tick() {
        val now = clock.instant()
        val (due, lastSlot) = sessions.system { session -> due(session, now) to lastSlot(session) }
        val slots = CatchUpSlots(maxOf(now, lastSlot?.plus(settings.catchUpSpacing) ?: now), settings.catchUpSpacing)
        for (schedule in due) {
            runCatching { fire(schedule, now, slots) }
                .onFailure { log.warn("Schedule {} failed to fire; retrying at the next tick", schedule.id, it) }
        }
    }

    private fun due(
        session: Session,
        now: Instant,
    ): List<InTenant> =
        session
            .createNativeQuery(DUE, Array<Any>::class.java)
            .setParameter("now", now)
            .setParameter("batch", settings.batch)
            .list()
            .map { InTenant(it[0] as UUID, it[1] as UUID) }

    private fun lastSlot(session: Session): Instant? {
        val query = session.createNativeQuery(LAST_SLOT, Instant::class.java)
        return query.uniqueResult()
    }

    private fun fire(
        schedule: InTenant,
        now: Instant,
        slots: CatchUpSlots,
    ) {
        val created =
            sessions.inTenant(schedule.tenantId) { session ->
                val record =
                    session
                        .createNativeQuery(LOCK, ScheduleRecord::class.java)
                        .setParameter("tenant", schedule.tenantId)
                        .setParameter("id", schedule.id)
                        .setParameter("now", now)
                        .uniqueResult()
                record?.let { Firing(session, it, now).fire(slots) }.orEmpty()
            }
        created.forEach { queued.onQueued(schedule.tenantId, it.agentId) }
    }

    /** One locked schedule at [now], inside its transaction. */
    private inner class Firing(
        private val session: Session,
        private val schedule: ScheduleRecord,
        private val now: Instant,
    ) {
        /** The catch-up first if its slot came, then the cron; returns the runs created. */
        fun fire(slots: CatchUpSlots): List<RunView> {
            val catchUp = schedule.catchUpAt?.takeIf { it <= now }?.let(::catchUp)
            val cron = schedule.nextRunAt?.let { Due.of(CronSchedule.parse(schedule.cron, schedule.timezone), it, now) }
            return listOfNotNull(catchUp, cron?.let { onCron(it, slots) })
        }

        private fun catchUp(at: Instant): RunView? {
            schedule.catchUpAt = null
            return attempt(at, FireKind.CATCH_UP)
        }

        private fun onCron(
            due: Due,
            slots: CatchUpSlots,
        ): RunView? =
            when (due) {
                Due.NotYet -> null
                is Due.OnTime -> attempt(due.at, FireKind.SCHEDULE).also { schedule.nextRunAt = due.next }
                is Due.Missed -> downtime(due, slots)
            }

        /** One row for the whole downtime; a catch-up is owed, at the earlier slot if one is owed already. */
        private fun downtime(
            due: Due.Missed,
            slots: CatchUpSlots,
        ): RunView? {
            schedule.nextRunAt = due.next
            schedule.catchUpAt = schedule.catchUpAt ?: slots.take()
            journal(FireKind.SCHEDULE, due.first, Result(FireOutcome.SKIPPED_DOWNTIME), due)
            return null
        }

        private fun attempt(
            at: Instant,
            kind: FireKind,
        ): RunView? {
            val result =
                try {
                    val run = runs.startScheduled(session, schedule.sourceId, kind.trigger, schedule.id)
                    Result(FireOutcome.RUN_CREATED, run = run)
                } catch (e: RunActive) {
                    Result(FireOutcome.SKIPPED_ACTIVE, activeRunId = e.activeRunId)
                } catch (e: RunsException) {
                    refusal(e)
                }
            schedule.skippedInRow = if (result.outcome.skip) schedule.skippedInRow + 1 else 0
            schedule.lastFiredAt = now
            journal(kind, at, result, null)
            return result.run
        }

        private fun journal(
            kind: FireKind,
            at: Instant,
            result: Result,
            missed: Due.Missed?,
        ) {
            val alert = result.outcome.skip && schedule.skippedInRow == settings.skipAlertThreshold
            val record =
                ScheduleFireRecord(
                    id = ids.next(),
                    scheduleId = schedule.id,
                    kind = kind.stored,
                    scheduledFor = at,
                    outcome = result.outcome.stored,
                    runId = result.run?.id ?: result.activeRunId,
                    reason = result.reason?.stored,
                    missedCount = missed?.count,
                    missedUntil = missed?.last,
                    skippedInRow = schedule.skippedInRow,
                    alert = alert,
                    recordedAt = now,
                )
            session.persist(record)
        }
    }

    private fun refusal(e: RunsException): Result {
        val (outcome, reason) = REFUSALS[e::class] ?: throw e
        return Result(outcome, reason = reason)
    }

    private class Result(
        val outcome: FireOutcome,
        val run: RunView? = null,
        val activeRunId: UUID? = null,
        val reason: FireReason? = null,
    )
}

/** Catch-up slots handed out within one tick, [spacing] apart from [next]. */
internal class CatchUpSlots(
    private var next: Instant,
    private val spacing: Duration,
) {
    fun take(): Instant = next.also { next = next.plus(spacing) }
}
