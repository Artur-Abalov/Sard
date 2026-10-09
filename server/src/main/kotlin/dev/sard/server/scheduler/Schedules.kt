// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import dev.sard.server.persistence.PageKey
import dev.sard.server.persistence.RunRecord
import dev.sard.server.persistence.ScheduleFireRecord
import dev.sard.server.persistence.ScheduleRecord
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.runs.RunState
import dev.sard.server.runs.Trigger
import dev.sard.server.runs.liveSource
import jakarta.persistence.LockModeType
import org.hibernate.Session
import org.hibernate.exception.ConstraintViolationException
import java.time.Clock
import java.time.Instant
import java.util.UUID

private const val SOURCE_KEY = "schedules_tenant_id_source_id_key"
private const val OF_SOURCE = "from ScheduleRecord where sourceId = :source"
private const val FIRES_OF = "from ScheduleFireRecord f where f.scheduleId = :schedule"
private const val LAST_RUN = "from RunRecord r where r.scheduleId = :schedule order by r.queuedAt desc, r.id desc"
private const val NEWEST_FIRST = "order by f.recordedAt desc, f.id desc"

/**
 * The schedule of a source (F3a): one per source, set as a whole. Setting it again unchanged keeps
 * its next fire; a change of cron, zone or enabled starts over from now, without a catch-up (answer 9).
 * Only the schedule's row is locked, never the source's: the scheduler locks the schedule first and
 * the source second, so taking them the other way round here could deadlock with it.
 */
class Schedules(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val ids: UuidV7,
) {
    /** Creates or replaces the schedule of [sourceId]; throws [InvalidSchedule] or SourceNotFound. */
    fun set(
        tenantId: UUID,
        sourceId: UUID,
        draft: ScheduleDraft,
    ): ScheduleView {
        val fires = CronSchedule.parse(draft.cron, draft.timezone)
        return try {
            sessions.inTenant(tenantId) { session -> view(session, write(session, sourceId, fires, draft)) }
        } catch (e: ConstraintViolationException) {
            // Two first sets of one source raced on the key: the loser replaces what the winner created.
            if (e.constraintName != SOURCE_KEY) throw e
            set(tenantId, sourceId, draft)
        }
    }

    /** The schedule of the live source [sourceId]; null if it has none. Throws SourceNotFound. */
    fun get(
        tenantId: UUID,
        sourceId: UUID,
    ): ScheduleView? =
        sessions.inTenant(tenantId) { session ->
            liveSource(session, sourceId, LockModeType.NONE)
            scheduleOf(session, sourceId, LockModeType.NONE)?.let { view(session, it) }
        }

    /**
     * The journal of the live source [sourceId], newest recorded first, after [after] if given, at most [limit];
     * empty without a schedule. Throws SourceNotFound.
     */
    fun fires(
        tenantId: UUID,
        sourceId: UUID,
        after: PageKey?,
        limit: Int,
    ): List<FireView> =
        sessions.inTenant(tenantId) { session ->
            liveSource(session, sourceId, LockModeType.NONE)
            val schedule = scheduleOf(session, sourceId, LockModeType.NONE) ?: return@inTenant emptyList()
            val paging = after?.let { " and ${PageKey.condition("f.recordedAt", "f.id")}" }.orEmpty()
            val query =
                session
                    .createSelectionQuery("$FIRES_OF$paging $NEWEST_FIRST", ScheduleFireRecord::class.java)
                    .setParameter("schedule", schedule.id)
            after?.bind(query)
            query.setMaxResults(limit).list().map(::fireView)
        }

    private fun write(
        session: Session,
        sourceId: UUID,
        fires: CronSchedule,
        draft: ScheduleDraft,
    ): ScheduleRecord {
        liveSource(session, sourceId, LockModeType.NONE)
        val now = clock.instant()
        val existing = scheduleOf(session, sourceId, LockModeType.PESSIMISTIC_WRITE)
        if (existing != null) return existing.also { replace(it, fires, draft, now) }
        val next = next(fires, draft.enabled, now)
        val record = ScheduleRecord(ids.next(), sourceId, fires.cron, fires.zone.id, draft.enabled, next, now, now)
        record.notifyOnSuccess = draft.notifyOnSuccess
        session.persist(record)
        session.flush()
        return record
    }

    private fun replace(
        record: ScheduleRecord,
        fires: CronSchedule,
        draft: ScheduleDraft,
        now: Instant,
    ) {
        if (record.notifyOnSuccess != draft.notifyOnSuccess) {
            record.notifyOnSuccess = draft.notifyOnSuccess
            record.updatedAt = now
        }
        if (changes(record, fires, draft)) reschedule(record, fires, draft, now)
    }

    private fun changes(
        record: ScheduleRecord,
        fires: CronSchedule,
        draft: ScheduleDraft,
    ): Boolean = record.cron != fires.cron || record.timezone != fires.zone.id || record.enabled != draft.enabled

    /** A change of cron, zone or enabled starts over from [now], without a catch-up. */
    private fun reschedule(
        record: ScheduleRecord,
        fires: CronSchedule,
        draft: ScheduleDraft,
        now: Instant,
    ) {
        record.cron = fires.cron
        record.timezone = fires.zone.id
        record.enabled = draft.enabled
        record.nextRunAt = next(fires, draft.enabled, now)
        record.catchUpAt = null
        record.updatedAt = now
    }

    private fun next(
        fires: CronSchedule,
        enabled: Boolean,
        now: Instant,
    ): Instant? = if (enabled) fires.nextAfter(now) else null

    private fun scheduleOf(
        session: Session,
        sourceId: UUID,
        lock: LockModeType,
    ): ScheduleRecord? =
        session
            .createSelectionQuery(OF_SOURCE, ScheduleRecord::class.java)
            .setParameter("source", sourceId)
            .setLockMode(lock)
            .uniqueResult()
}

private fun fireView(record: ScheduleFireRecord) =
    FireView(
        id = record.id,
        kind = FireKind.of(record.kind),
        scheduledFor = record.scheduledFor,
        outcome = FireOutcome.of(record.outcome),
        runId = record.runId,
        reason = record.reason?.let(FireReason::of),
        missedCount = record.missedCount,
        missedUntil = record.missedUntil,
        missedCountCapped = record.missedCountCapped,
        skippedInRow = record.skippedInRow,
        alert = record.alert,
        recordedAt = record.recordedAt,
    )

private fun view(
    session: Session,
    record: ScheduleRecord,
) = view(record, lastRunOf(session, record.id))

private fun lastRunOf(
    session: Session,
    scheduleId: UUID,
): LastRun? =
    session
        .createSelectionQuery(LAST_RUN, RunRecord::class.java)
        .setParameter("schedule", scheduleId)
        .setMaxResults(1)
        .uniqueResult()
        ?.let { LastRun(it.id, Trigger.of(it.trigger), RunState.of(it.status), it.queuedAt, it.finishedAt) }

internal fun view(
    record: ScheduleRecord,
    lastRun: LastRun?,
) = ScheduleView(
    id = record.id,
    sourceId = record.sourceId,
    cron = record.cron,
    timezone = record.timezone,
    enabled = record.enabled,
    nextRunAt = record.nextRunAt,
    catchUpAt = record.catchUpAt,
    lastFiredAt = record.lastFiredAt,
    skippedInRow = record.skippedInRow,
    notifyOnSuccess = record.notifyOnSuccess,
    lastRun = lastRun,
    createdAt = record.createdAt,
    updatedAt = record.updatedAt,
)
