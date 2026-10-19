// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.scheduler.Fires
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.Instant
import java.time.temporal.ChronoUnit
import kotlin.test.Test
import kotlin.test.assertEquals

/** Every night at 04:30 UTC; a fake, so the mutants of this test stay in CycleMoments, not in the cron parser. */
private val NIGHTLY =
    Fires { after ->
        val today = after.truncatedTo(ChronoUnit.DAYS).plus(Duration.ofMinutes(4 * 60 + 30))
        if (today > after) today else today.plus(Duration.ofDays(1))
    }
private val EVERY_MINUTE = Fires { it.truncatedTo(ChronoUnit.MINUTES).plus(Duration.ofMinutes(1)) }
private val DAY_START: Instant = Instant.parse("2026-10-19T00:00:00Z")
private val TODAY_0430: Instant = Instant.parse("2026-10-19T04:30:00Z")
private val TOMORROW_0430: Instant = Instant.parse("2026-10-20T04:30:00Z")

/** V-D9: the cycle's last moment at or before now, and the next one, by the installation's cron. */
@MutFlowTest
class CycleMomentsTest {
    @Test
    fun `after tonight's moment the cycle is that moment and the next is tomorrow night`() {
        val now = Instant.parse("2026-10-19T10:00:00Z")

        val moments = MutFlow.underTest { CycleMoments.of(NIGHTLY, now - Duration.ofDays(32), now) }

        assertEquals(CycleMoments(TODAY_0430, TOMORROW_0430), moments)
    }

    @Test
    fun `the moment itself is already the cycle`() {
        val moments = MutFlow.underTest { CycleMoments.of(NIGHTLY, DAY_START, TODAY_0430) }

        assertEquals(CycleMoments(TODAY_0430, TOMORROW_0430), moments)
    }

    @Test
    fun `before the first moment since the schedule changed there is no cycle yet`() {
        val changed = Instant.parse("2026-10-19T05:00:00Z")

        val moments = MutFlow.underTest { CycleMoments.of(NIGHTLY, changed, Instant.parse("2026-10-19T12:00:00Z")) }

        assertEquals(CycleMoments(null, TOMORROW_0430), moments)
    }

    @Test
    fun `a moment exactly at the change does not count, the change starts after it`() {
        val moments = MutFlow.underTest { CycleMoments.of(NIGHTLY, TODAY_0430, TODAY_0430) }

        assertEquals(CycleMoments(null, TOMORROW_0430), moments)
    }

    @Test
    fun `counting stops at the limit, the next moment is still the one after now`() {
        val now = DAY_START + Duration.ofMinutes(CycleMoments.MOMENT_LIMIT + 500L)

        val moments = MutFlow.underTest { CycleMoments.of(EVERY_MINUTE, DAY_START, now) }

        val lastCounted = DAY_START + Duration.ofMinutes(CycleMoments.MOMENT_LIMIT.toLong())
        assertEquals(CycleMoments(lastCounted, now + Duration.ofMinutes(1)), moments)
    }

    @Test
    fun `a moment is still current until the next one comes`() {
        val current = CycleMoments(TODAY_0430, TOMORROW_0430)

        assertEquals(true, MutFlow.underTest { current.currentAt(TOMORROW_0430.minusNanos(1)) })
        assertEquals(false, MutFlow.underTest { current.currentAt(TOMORROW_0430) })
    }
}
