// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import java.time.Instant

/**
 * What a schedule owes at a moment (D16, F3a decision 7). A fire whose successor is still ahead is
 * on time, however late; two or more passed fires mean the server was down: one catch-up stands
 * for all of them, and the schedule moves to the first fire ahead.
 */
sealed interface Due {
    data object NotYet : Due

    /** Fire [at] as a scheduled run; the schedule's next fire is [next]. */
    data class OnTime(
        val at: Instant,
        val next: Instant,
    ) : Due

    /** [count] fires from [first] through [last] have passed (at most [MISSED_COUNT_LIMIT]); the next is [next]. */
    data class Missed(
        val first: Instant,
        val count: Int,
        val last: Instant,
        val next: Instant,
    ) : Due

    companion object {
        /** Counting stops here: a schedule every minute down for a week would otherwise count 10 080. */
        const val MISSED_COUNT_LIMIT = 10_000

        fun of(
            schedule: Fires,
            nextRunAt: Instant,
            now: Instant,
        ): Due =
            when {
                nextRunAt > now -> NotYet
                schedule.nextAfter(nextRunAt) > now -> OnTime(nextRunAt, schedule.nextAfter(nextRunAt))
                else -> missed(schedule, nextRunAt, now)
            }

        private fun missed(
            schedule: Fires,
            first: Instant,
            now: Instant,
        ): Missed {
            val passed =
                generateSequence(first, schedule::nextAfter)
                    .take(MISSED_COUNT_LIMIT)
                    .takeWhile { it <= now }
                    .toList()
            return Missed(first, passed.size, passed.last(), schedule.nextAfter(now))
        }
    }
}
