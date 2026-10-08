// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals

/** What a schedule owes at a moment (D16, F3a answer 7): nothing, one fire, or one catch-up for a downtime. */
@MutFlowTest
class DueTest {
    private val everyMinute = CronSchedule.parse("* * * * *", "UTC")
    private val at = Instant.parse("2026-10-08T10:00:00Z")

    private fun due(
        nextRunAt: Instant,
        now: Instant,
        schedule: CronSchedule = everyMinute,
    ) = MutFlow.underTest { Due.of(schedule, nextRunAt, now) }

    @Test
    fun `a fire still in the future is not due`() {
        assertEquals(Due.NotYet, due(at, at.minusMillis(1)))
    }

    @Test
    fun `a fire due exactly now fires on time and moves to the next one`() {
        assertEquals(Due.OnTime(at, at.plusSeconds(60)), due(at, at))
    }

    @Test
    fun `a late fire whose successor is still ahead is on time, however late`() {
        val daily = CronSchedule.parse("0 3 * * *", "UTC")
        val nextRunAt = Instant.parse("2026-10-08T03:00:00Z")
        val now = Instant.parse("2026-10-09T02:59:59Z")
        assertEquals(Due.OnTime(nextRunAt, Instant.parse("2026-10-09T03:00:00Z")), due(nextRunAt, now, daily))
    }

    @Test
    fun `two or more passed fires are a downtime, owed as one catch-up counting all of them`() {
        assertEquals(Due.Missed(at, 2, at.plusSeconds(120)), due(at, at.plusSeconds(60)))
        assertEquals(Due.Missed(at, 3, at.plusSeconds(180)), due(at, at.plusSeconds(150)))
    }

    @Test
    fun `the count of passed fires stops at the limit, the next fire is still the first one ahead`() {
        val now = at.plusSeconds(60L * (Due.MISSED_COUNT_LIMIT + 500))
        val next = now.plusSeconds(60)
        assertEquals(Due.Missed(at, Due.MISSED_COUNT_LIMIT, next), due(at, now))
    }
}
