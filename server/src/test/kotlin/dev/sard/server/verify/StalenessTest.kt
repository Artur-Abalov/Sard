// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

private val NOW: Instant = Instant.parse("2026-10-19T12:00:00Z")
private val AFTER: Duration = Duration.ofDays(7)
private val PASS = UUID(0, 1)
private val EPISODE = UUID(0, 2)

/** V4 events: «not verified successfully for longer than N days», one event per turn (strategy 6). */
@MutFlowTest
class StalenessTest {
    private fun judge(facts: StaleFacts) = MutFlow.underTest { Staleness.judge(facts, NOW, AFTER) }

    @Test
    fun `a source passed more than N days ago opens an episode counted from that pass`() {
        val passed = NOW - AFTER - Duration.ofSeconds(1)

        assertEquals(StaleTurn.Open(passed), judge(StaleFacts(passed, PASS, NOW - Duration.ofDays(30), null)))
    }

    @Test
    fun `exactly N days is not yet longer than N days`() {
        assertEquals(StaleTurn.Keep, judge(StaleFacts(NOW - AFTER, PASS, NOW - Duration.ofDays(30), null)))
    }

    @Test
    fun `a source never passed counts from its first whole snapshot`() {
        val first = NOW - Duration.ofDays(8)

        assertEquals(StaleTurn.Open(first), judge(StaleFacts(null, null, first, null)))
        assertEquals(StaleTurn.Keep, judge(StaleFacts(null, null, NOW - Duration.ofDays(6), null)))
    }

    @Test
    fun `a source without a whole snapshot has nothing to verify and never goes stale`() {
        assertEquals(StaleTurn.Keep, judge(StaleFacts(null, null, null, null)))
    }

    @Test
    fun `an open episode stays open while the source stays stale, no second event`() {
        val facts = StaleFacts(NOW - Duration.ofDays(9), PASS, NOW - Duration.ofDays(30), EPISODE)

        assertEquals(StaleTurn.Keep, judge(facts))
    }

    @Test
    fun `a pass within N days closes the open episode with that pass`() {
        val facts = StaleFacts(NOW - Duration.ofHours(1), PASS, NOW - Duration.ofDays(30), EPISODE)

        assertEquals(StaleTurn.Close(EPISODE, PASS), judge(facts))
    }
}
