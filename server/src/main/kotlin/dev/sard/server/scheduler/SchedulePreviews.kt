// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.time.ZoneId

/** What the console shows of a schedule before it is saved (F3b, K1). */
data class PreviewedSchedule(
    val cron: String,
    val timezone: String,
    val description: String,
    val nextFires: List<Instant>,
    val tooFrequent: Boolean,
)

/**
 * Previews a schedule without storing it (F3b). Words, the nearest fires and the frequency warning
 * are the server's, so the console never evaluates a cron (decisions 1 and 12). [serverZone] is the
 * zone a schedule without one is read in; a JVM zone that is no IANA region (an offset) is UTC.
 */
class SchedulePreviews(
    private val clock: Clock,
    serverZone: ZoneId,
) {
    private val defaultZone = if (serverZone.id in ZoneId.getAvailableZoneIds()) serverZone.id else "UTC"

    /** Throws [InvalidSchedule] exactly as setting the schedule would. */
    fun of(
        cron: String,
        timezone: String?,
        language: ScheduleLanguage,
    ): PreviewedSchedule {
        val schedule = CronSchedule.parse(cron, timezone ?: defaultZone)
        val fires = upcoming(schedule, clock.instant())
        return PreviewedSchedule(
            schedule.cron,
            schedule.zone.id,
            ScheduleDescription.of(schedule.cron, language),
            fires.take(SHOWN),
            fires.zipWithNext().any { (a, b) -> Duration.between(a, b) < TOO_CLOSE },
        )
    }

    /** The next [CHECKED] fires after [now]; fewer if the cron has no more within reach. */
    private fun upcoming(
        schedule: CronSchedule,
        now: Instant,
    ): List<Instant> = generateSequence(next(schedule, now)) { next(schedule, it) }.take(CHECKED).toList()

    private fun next(
        schedule: CronSchedule,
        after: Instant,
    ): Instant? = runCatching { schedule.nextAfter(after) }.getOrNull()

    private companion object {
        const val SHOWN = 3
        const val CHECKED = 100
        val TOO_CLOSE: Duration = Duration.ofMinutes(15)
    }
}
