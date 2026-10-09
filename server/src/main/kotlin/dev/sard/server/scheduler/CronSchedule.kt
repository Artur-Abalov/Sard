// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import org.springframework.scheduling.support.CronExpression
import java.time.Instant
import java.time.LocalDateTime
import java.time.ZoneId

/** The input of a schedule that is wrong; the API answers 422 on that field. */
enum class ScheduleField { CRON, TIMEZONE }

/** A schedule the server refuses: not five standard cron fields, a cron that never fires, or not an IANA zone. */
class InvalidSchedule(
    val field: ScheduleField,
    message: String,
    cause: Throwable? = null,
) : RuntimeException(message, cause)

/** When a schedule fires; the scheduler depends on this, tests on fakes. */
fun interface Fires {
    /** The first fire strictly after [after]. */
    fun nextAfter(after: Instant): Instant
}

/**
 * A standard 5-field cron read in an IANA zone (F3a, decisions 5 and 6). Spring's [CronExpression]
 * matches the fields; this class adds what standard (Vixie) cron does and Spring does not:
 * - restricted day of month and day of week fire on either of them, not on both;
 * - a starred step in day of week counts from Sunday as 0;
 * - with a fixed hour, times skipped by a DST gap fire once at the transition and times repeated by
 *   an overlap fire in their first occurrence only; a starred hour follows real time.
 */
class CronSchedule private constructor(
    /** The five fields, single-spaced. */
    val cron: String,
    val zone: ZoneId,
    private val expressions: List<CronExpression>,
    private val wallClock: Boolean,
) : Fires {
    override fun nextAfter(after: Instant): Instant = if (wallClock) nextOnWallClock(after) else nextInRealTime(after)

    private fun nextInRealTime(after: Instant): Instant {
        val zoned = after.atZone(zone)
        return expressions.mapNotNull { it.next(zoned) }.minOf { it.toInstant() }
    }

    /**
     * Walks local times from [after]'s own, which maps back to [after] or earlier and so never
     * qualifies. Local times at or before [after] once mapped (a gap collapsed onto its transition,
     * the second occurrence of an overlap) are skipped; a DST shift skips at most a few hours of them.
     */
    private fun nextOnWallClock(after: Instant): Instant =
        generateSequence(LocalDateTime.ofInstant(after, zone), ::nextLocal)
            .take(SKIPPED_LOCAL_LIMIT)
            .map(::earliestInstant)
            .first { it > after }

    private fun nextLocal(after: LocalDateTime): LocalDateTime = expressions.mapNotNull { it.next(after) }.min()

    /** A local time in a gap happens at the transition; one in an overlap at its earlier offset. */
    private fun earliestInstant(local: LocalDateTime): Instant {
        val transition = zone.rules.getTransition(local)
        return if (transition?.isGap == true) transition.instant else local.atZone(zone).toInstant()
    }

    companion object {
        private const val FIELDS = 5
        private const val MAX_LENGTH = 200
        private const val SKIPPED_LOCAL_LIMIT = 1_000
        private const val HOUR = 1
        private const val DAY_OF_MONTH = 2
        private const val MONTH = 3
        private const val DAY_OF_WEEK = 4
        private val MONTHS = setOf("JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC")
        private val DAYS = setOf("SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT")
        private val CHARACTERS = Regex("[0-9A-Za-z*,/-]+")
        private val WORDS = Regex("[A-Za-z]+")
        private val SPACES = Regex("\\s+")

        /** Parses [cron] in [zone]; throws [InvalidSchedule] naming the wrong field. */
        fun parse(
            cron: String,
            zone: String,
        ): CronSchedule {
            val zoneId = zoneOf(zone)
            val fields = fieldsOf(cron)
            // The column holds 200 characters; the message does not echo the input.
            if (fields.joinToString(" ").length > MAX_LENGTH) {
                throw InvalidSchedule(ScheduleField.CRON, "cron is longer than $MAX_LENGTH characters")
            }
            val schedule =
                try {
                    CronSchedule(fields.joinToString(" "), zoneId, expressionsOf(fields), !fields[HOUR].startsWith('*'))
                } catch (e: IllegalArgumentException) {
                    throw InvalidSchedule(ScheduleField.CRON, "cron \"$cron\": ${e.message}", e)
                }
            return schedule.also { it.requireFires() }
        }

        private fun zoneOf(zone: String): ZoneId {
            if (zone !in ZoneId.getAvailableZoneIds()) {
                throw InvalidSchedule(ScheduleField.TIMEZONE, "\"$zone\" is not an IANA time zone")
            }
            return ZoneId.of(zone)
        }

        private fun fieldsOf(cron: String): List<String> {
            val fields = cron.trim().split(SPACES)
            if (fields.size != FIELDS) {
                throw InvalidSchedule(ScheduleField.CRON, "cron \"$cron\" has ${fields.size} fields, not $FIELDS")
            }
            fields.forEachIndexed { index, field -> requireStandard(field, namesOf(index)) }
            return fields
        }

        private fun namesOf(index: Int): Set<String> =
            when (index) {
                MONTH -> MONTHS
                DAY_OF_WEEK -> DAYS
                else -> emptySet()
            }

        /** Rejects what standard cron lacks: Quartz's ?, L, W and #, macros, misplaced or long names. */
        private fun requireStandard(
            field: String,
            names: Set<String>,
        ) {
            val standard = CHARACTERS.matches(field) && WORDS.findAll(field).all { it.value.uppercase() in names }
            if (!standard) throw InvalidSchedule(ScheduleField.CRON, "cron field \"$field\" is not standard cron")
        }

        /** Spring's six fields; two expressions whose earliest fire wins when both day fields are restricted. */
        private fun expressionsOf(fields: List<String>): List<CronExpression> {
            val time = "0 " + fields.subList(0, DAY_OF_MONTH).joinToString(" ")
            val dayOfMonth = fields[DAY_OF_MONTH]
            val month = fields[MONTH]
            val dayOfWeek = fromSunday(fields[DAY_OF_WEEK])
            val either = !dayOfMonth.startsWith('*') && !fields[DAY_OF_WEEK].startsWith('*')
            val texts =
                if (either) {
                    listOf("$time $dayOfMonth $month *", "$time * $month $dayOfWeek")
                } else {
                    listOf("$time $dayOfMonth $month $dayOfWeek")
                }
            return texts.map(CronExpression::parse)
        }

        /** Standard cron's day of week star is 0-7; Spring's is 1-7, which shifts a starred step. */
        private fun fromSunday(dayOfWeek: String): String =
            dayOfWeek.split(',').joinToString(",") { if (it.startsWith('*')) "0-7" + it.drop(1) else it }
    }

    private fun requireFires() {
        try {
            nextAfter(Instant.EPOCH)
        } catch (_: NoSuchElementException) {
            throw InvalidSchedule(ScheduleField.CRON, "cron \"$cron\" never fires")
        }
    }

    override fun toString() = "CronSchedule($cron, $zone)"
}
