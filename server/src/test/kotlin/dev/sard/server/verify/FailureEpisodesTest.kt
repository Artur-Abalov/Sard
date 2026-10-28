// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals

private fun at(day: Int): Instant = Instant.parse("2026-10-%02dT04:30:00Z".format(day))

private fun record(
    status: VerificationStatus,
    day: Int,
) = HistoryMark(status, at(day))

/** V6: failures of a source and the pass that fixed them, for the report. */
@MutFlowTest
class FailureEpisodesTest {
    private fun episodes(vararg marks: HistoryMark) = MutFlow.underTest { FailureEpisodes.of(marks.toList()) }

    @Test
    fun `failures in a row and the pass after them are one episode with its fix`() {
        val episodes =
            episodes(
                record(VerificationStatus.FAILED, 10),
                record(VerificationStatus.FAILED, 11),
                record(VerificationStatus.PASSED, 12),
            )

        assertEquals(listOf(FailureEpisode(at(10), 2, at(12))), episodes)
    }

    @Test
    fun `a failure nothing has fixed has no fix`() {
        val episodes = episodes(record(VerificationStatus.PASSED, 10), record(VerificationStatus.FAILED, 20))

        assertEquals(listOf(FailureEpisode(at(20), 1, null)), episodes)
    }

    @Test
    fun `a verification that did not take place neither ends an episode nor starts one`() {
        val episodes =
            episodes(
                record(VerificationStatus.CANNOT_VERIFY, 9),
                record(VerificationStatus.FAILED, 10),
                record(VerificationStatus.NO_VERIFIER, 11),
                record(VerificationStatus.INFRASTRUCTURE_FAILURE, 12),
                record(VerificationStatus.FAILED, 13),
                record(VerificationStatus.PASSED, 14),
            )

        assertEquals(listOf(FailureEpisode(at(10), 2, at(14))), episodes)
    }

    @Test
    fun `a second series after a pass is a second episode`() {
        val episodes =
            episodes(
                record(VerificationStatus.FAILED, 1),
                record(VerificationStatus.PASSED, 2),
                record(VerificationStatus.FAILED, 3),
            )

        assertEquals(listOf(FailureEpisode(at(1), 1, at(2)), FailureEpisode(at(3), 1, null)), episodes)
    }

    @Test
    fun `no failures, no episodes`() {
        assertEquals(emptyList(), episodes(record(VerificationStatus.PASSED, 1)))
        assertEquals(emptyList(), episodes())
    }

    @Test
    fun `an episode belongs to a period it overlaps, until it is fixed`() {
        val period = ReportPeriod(at(10), at(20))

        fun overlaps(episode: FailureEpisode) = MutFlow.underTest { episode.overlaps(period) }

        assertEquals(true, overlaps(FailureEpisode(at(12), 1, at(14))))
        assertEquals(true, overlaps(FailureEpisode(at(5), 1, at(10))), "fixed when the period begins")
        assertEquals(false, overlaps(FailureEpisode(at(5), 1, at(9))), "fixed before the period")
        assertEquals(true, overlaps(FailureEpisode(at(5), 1, null)), "still not fixed")
        assertEquals(false, overlaps(FailureEpisode(at(20), 1, null)), "starts when the period ends")
        assertEquals(true, overlaps(FailureEpisode(at(19), 1, null)))
    }
}
