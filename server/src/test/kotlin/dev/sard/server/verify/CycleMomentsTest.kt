// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.scheduler.CronSchedule
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals

private val NIGHTLY = CronSchedule.parse("30 4 * * *", "UTC")
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
    fun `a moment is still current until the next one comes`() {
        val current = CycleMoments(TODAY_0430, TOMORROW_0430)

        assertEquals(true, MutFlow.underTest { current.currentAt(TOMORROW_0430.minusNanos(1)) })
        assertEquals(false, MutFlow.underTest { current.currentAt(TOMORROW_0430) })
    }
}
