// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.scheduler.CronSchedule
import dev.sard.server.scheduler.Fires
import org.hibernate.Session
import java.time.Duration
import java.time.Instant

// verification_schedule is installation-wide (no tenant_id): one row at most.
private const val READ = "select cron, timezone, updated_at, stale_after_days from verification_schedule"
private const val WRITE = """
    insert into verification_schedule (singleton, cron, timezone, updated_at, stale_after_days)
    values (true, :cron, :zone, :now, :stale)
    on conflict (singleton) do update set cron = excluded.cron, timezone = excluded.timezone,
        updated_at = excluded.updated_at,
        stale_after_days = coalesce(excluded.stale_after_days, verification_schedule.stale_after_days)
    returning cron, timezone, stale_after_days"""

/** A threshold outside [STALE_AFTER_DAYS]: 422 at staleAfterDays. */
class InvalidStaleThreshold(
    val days: Int,
) : RuntimeException("staleAfterDays must be from ${STALE_AFTER_DAYS.first} to ${STALE_AFTER_DAYS.last}, not $days")

/** The furthest back a cycle moment is looked for: a monthly cron's. */
private val LOOK_BACK: Duration = Duration.ofDays(32)

/** The cycle's cron as set; moments before [since] do not count. [staleAfterDays] is the threshold in force. */
data class CycleSchedule(
    val cron: String,
    val timezone: String,
    val since: Instant,
    val staleAfterDays: Int,
) {
    val fires: Fires get() = CronSchedule.parse(cron, timezone)
}

/**
 * The daily cycle of the installation (V-D9): one schedule for all tenants, the defaults until REST sets it. A change
 * starts from its own moment: the moments before it do not count, so changing the time never fires a missed cycle.
 */
class VerificationSchedule(
    private val settings: VerificationSettings,
) {
    fun current(session: Session): CycleSchedule {
        val row = session.createNativeQuery(READ, Array<Any?>::class.java).list().firstOrNull()
        val columns = row ?: arrayOf<Any?>(settings.defaultCron, settings.defaultTimezone, Instant.EPOCH, null)
        val (cron, zone, since) = columns
        return CycleSchedule(cron as String, zone as String, since as Instant, staleDays(columns.last()))
    }

    /** The threshold in force: the saved one, or the server property's until the console saves one. */
    private fun staleDays(saved: Any?): Int = (saved as Number?)?.toInt() ?: settings.staleAfterDays

    /** The cycle's last moment at or before [now] and its next one. */
    fun moments(
        session: Session,
        now: Instant,
    ): CycleMoments = moments(current(session), now)

    /**
     * Sets the cron and zone, and the threshold when [staleAfterDays] is given (null leaves it). Throws InvalidSchedule
     * naming the wrong field, or [InvalidStaleThreshold], and changes nothing then. One statement writes all three and
     * returns what it wrote, so two saves at once leave one of them whole.
     */
    fun set(
        session: Session,
        cron: String,
        timezone: String,
        staleAfterDays: Int?,
        now: Instant,
    ): CycleSchedule {
        CronSchedule.parse(cron, timezone)
        if (staleAfterDays != null && staleAfterDays !in STALE_AFTER_DAYS) throw InvalidStaleThreshold(staleAfterDays)
        val row =
            session
                .createNativeQuery(WRITE.trimIndent(), Array<Any?>::class.java)
                .setParameter("cron", cron)
                .setParameter("zone", timezone)
                .setParameter("now", now)
                .setParameter("stale", staleAfterDays?.toShort(), Short::class.javaObjectType)
                .singleResult
        return CycleSchedule(row[0] as String, row[1] as String, now, staleDays(row[2]))
    }

    companion object {
        fun moments(
            schedule: CycleSchedule,
            now: Instant,
        ): CycleMoments = CycleMoments.of(schedule.fires, maxOf(schedule.since, now - LOOK_BACK), now)
    }
}
