// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.scheduler.CronSchedule
import dev.sard.server.scheduler.Fires
import org.hibernate.Session
import java.time.Duration
import java.time.Instant

// verification_schedule is installation-wide (no tenant_id): one row at most.
private const val READ = "select cron, timezone, updated_at from verification_schedule"
private const val WRITE = """
    insert into verification_schedule (singleton, cron, timezone, updated_at) values (true, :cron, :zone, :now)
    on conflict (singleton) do update set cron = excluded.cron, timezone = excluded.timezone,
        updated_at = excluded.updated_at"""

/** The furthest back a cycle moment is looked for: a monthly cron's. */
private val LOOK_BACK: Duration = Duration.ofDays(32)

/** The cycle's cron as set; moments before [since] do not count. */
data class CycleSchedule(
    val cron: String,
    val timezone: String,
    val since: Instant,
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
        val (cron, zone, since) = row ?: arrayOf(settings.defaultCron, settings.defaultTimezone, Instant.EPOCH)
        return CycleSchedule(cron as String, zone as String, since as Instant)
    }

    /** The cycle's last moment at or before [now] and its next one. */
    fun moments(
        session: Session,
        now: Instant,
    ): CycleMoments = moments(current(session), now)

    /** Sets the cron and zone; throws InvalidSchedule naming the wrong field and changes nothing then. */
    fun set(
        session: Session,
        cron: String,
        timezone: String,
        now: Instant,
    ): CycleSchedule {
        CronSchedule.parse(cron, timezone)
        session
            .createNativeMutationQuery(WRITE.trimIndent())
            .setParameter("cron", cron)
            .setParameter("zone", timezone)
            .setParameter("now", now)
            .executeUpdate()
        return CycleSchedule(cron, timezone, now)
    }

    companion object {
        fun moments(
            schedule: CycleSchedule,
            now: Instant,
        ): CycleMoments = CycleMoments.of(schedule.fires, maxOf(schedule.since, now - LOOK_BACK), now)
    }
}
