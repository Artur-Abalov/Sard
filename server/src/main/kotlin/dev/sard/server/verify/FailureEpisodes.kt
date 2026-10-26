// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import java.time.Instant

/** What the report needs of one record to tell episodes of failure. */
data class HistoryMark(
    val status: VerificationStatus,
    val at: Instant,
)

/** A series of failures: the first one's time, how many records failed in it, the pass that fixed it (null: none). */
data class FailureEpisode(
    val startedAt: Instant,
    val records: Int,
    val fixedAt: Instant?,
) {
    /** Whether the episode lasted into [period]: it began before its end and was not fixed before its start. */
    fun overlaps(period: ReportPeriod): Boolean {
        val fixedBefore = fixedAt != null && fixedAt.isBefore(period.from)
        return startedAt.isBefore(period.to) && !fixedBefore
    }
}

/**
 * The episodes of failure in a source's history, oldest first (V6, rule 20). A pass ends a series; a verification that
 * did not take place proves neither the backup nor its failure, so it neither ends a series nor starts one.
 */
object FailureEpisodes {
    fun of(history: List<HistoryMark>): List<FailureEpisode> {
        val episodes = mutableListOf<FailureEpisode>()
        var open: FailureEpisode? = null
        for (mark in history) {
            when (mark.status) {
                VerificationStatus.FAILED -> {
                    open = continued(open, mark.at)
                }

                VerificationStatus.PASSED -> {
                    open?.let { episodes += it.copy(fixedAt = mark.at) }
                    open = null
                }

                VerificationStatus.CANNOT_VERIFY,
                VerificationStatus.INFRASTRUCTURE_FAILURE,
                VerificationStatus.NO_VERIFIER,
                -> {
                    Unit
                }
            }
        }
        return episodes + listOfNotNull(open)
    }

    private fun continued(
        open: FailureEpisode?,
        at: Instant,
    ): FailureEpisode = open?.let { it.copy(records = it.records + 1) } ?: FailureEpisode(at, 1, null)
}
