// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.freshness

import dev.sard.server.scheduler.CronSchedule
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

private val NIGHTLY = CronSchedule.parse("0 3 * * *", "UTC")
private val HOURLY = CronSchedule.parse("0 * * * *", "UTC")
private val CHANGED: Instant = Instant.parse("2026-10-01T12:00:00Z")
private val SUCCESS: Instant = Instant.parse("2026-10-05T03:10:00Z")
private val RUN: UUID = UUID.fromString("0192f7a0-0000-7000-8000-000000000001")
private val EPISODE: UUID = UUID.fromString("0192f7a0-0000-7000-8000-000000000002")

private fun judged(
    source: SourceFreshness,
    at: Instant,
): Verdict = MutFlow.underTest { Freshness.judge(source, at) }

private fun deadlineOf(
    from: Instant,
    fires: CronSchedule,
): Instant = MutFlow.underTest { Freshness.deadline(from, fires) }

/** F6: "no success for too long" — 1.5 of the schedule's interval after the last success. */
@MutFlowTest(includeTargets = [Freshness::class])
class FreshnessTest {
    private fun source(
        fires: CronSchedule = NIGHTLY,
        success: LastSuccess? = LastSuccess(RUN, SUCCESS),
        open: OpenEpisode? = null,
    ) = SourceFreshness(UUID.randomUUID(), UUID.randomUUID(), fires, CHANGED, success, open)

    @Test
    fun `Срок - полтора интервала расписания после последнего успеха`() {
        // The next two fires after 03:10 on the 5th are 03:00 on the 6th and the 7th: 24 h apart.
        assertEquals(Instant.parse("2026-10-06T15:10:00Z"), deadlineOf(SUCCESS, NIGHTLY))
        assertEquals(Instant.parse("2026-10-05T04:40:00Z"), deadlineOf(SUCCESS, HOURLY))
    }

    @Test
    fun `До срока - ничего, в срок и после - эпизод с отсчётом от успеха`() {
        val deadline = Instant.parse("2026-10-06T15:10:00Z")
        assertEquals(Verdict.Keep, judged(source(), deadline.minusSeconds(1)))
        assertEquals(Verdict.Open(deadline, SUCCESS), judged(source(), deadline))
        assertEquals(Verdict.Open(deadline, SUCCESS), judged(source(), deadline.plusSeconds(60)))
    }

    @Test
    fun `Без единого успеха срок считается от последнего изменения расписания`() {
        val deadline = Freshness.deadline(CHANGED, NIGHTLY)
        assertEquals(Instant.parse("2026-10-03T00:00:00Z"), deadline)
        assertEquals(Verdict.Open(deadline, null), judged(source(success = null), deadline))
    }

    @Test
    fun `Изменение расписания позже успеха сдвигает отсчёт`() {
        val early = LastSuccess(RUN, Instant.parse("2026-09-20T03:10:00Z"))
        val deadline = Freshness.deadline(CHANGED, NIGHTLY)
        assertEquals(Verdict.Keep, judged(source(success = early), deadline.minusSeconds(1)))
        assertEquals(Verdict.Open(deadline, early.at), judged(source(success = early), deadline))
    }

    @Test
    fun `Открытый эпизод закрывает только успех новее того, от которого он отсчитан`() {
        val later = Instant.parse("2026-10-09T03:20:00Z")
        val counted = OpenEpisode(EPISODE, SUCCESS)
        assertEquals(Verdict.Keep, judged(source(open = counted), later))
        val fresh = LastSuccess(RUN, later)
        assertEquals(Verdict.Close(EPISODE, RUN), judged(source(success = fresh, open = counted), later))
    }

    @Test
    fun `Эпизод без успехов закрывает первый успех`() {
        val never = OpenEpisode(EPISODE, null)
        assertEquals(Verdict.Keep, judged(source(success = null, open = never), SUCCESS))
        assertEquals(Verdict.Close(EPISODE, RUN), judged(source(open = never), SUCCESS))
    }
}
