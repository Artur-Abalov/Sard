// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import dev.sard.server.persistence.ScheduleRecord
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.runs.liveSource
import jakarta.persistence.LockModeType
import org.hibernate.Session
import org.hibernate.exception.ConstraintViolationException
import java.time.Clock
import java.time.Instant
import java.util.UUID

private const val SOURCE_KEY = "schedules_tenant_id_source_id_key"
private const val OF_SOURCE = "from ScheduleRecord where sourceId = :source"

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
            sessions.inTenant(tenantId) { session -> view(write(session, sourceId, fires, draft.enabled)) }
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
            scheduleOf(session, sourceId, LockModeType.NONE)?.let(::view)
        }

    private fun write(
        session: Session,
        sourceId: UUID,
        fires: CronSchedule,
        enabled: Boolean,
    ): ScheduleRecord {
        liveSource(session, sourceId, LockModeType.NONE)
        val now = clock.instant()
        val existing = scheduleOf(session, sourceId, LockModeType.PESSIMISTIC_WRITE)
        if (existing != null) return existing.also { replace(it, fires, enabled, now) }
        val next = next(fires, enabled, now)
        val record = ScheduleRecord(ids.next(), sourceId, fires.cron, fires.zone.id, enabled, next, now, now)
        session.persist(record)
        session.flush()
        return record
    }

    private fun replace(
        record: ScheduleRecord,
        fires: CronSchedule,
        enabled: Boolean,
        now: Instant,
    ) {
        if (record.cron == fires.cron && record.timezone == fires.zone.id && record.enabled == enabled) return
        record.cron = fires.cron
        record.timezone = fires.zone.id
        record.enabled = enabled
        record.nextRunAt = next(fires, enabled, now)
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

internal fun view(record: ScheduleRecord) =
    ScheduleView(
        id = record.id,
        sourceId = record.sourceId,
        cron = record.cron,
        timezone = record.timezone,
        enabled = record.enabled,
        nextRunAt = record.nextRunAt,
        catchUpAt = record.catchUpAt,
        lastFiredAt = record.lastFiredAt,
        skippedInRow = record.skippedInRow,
        createdAt = record.createdAt,
        updatedAt = record.updatedAt,
    )
