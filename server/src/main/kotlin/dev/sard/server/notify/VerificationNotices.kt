// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.runs.StepOutput
import dev.sard.server.verify.VerificationStatus
import java.time.Instant
import java.util.UUID

/**
 * A restore verification record as a notification sees it (V6): read back at send time, with the names a person
 * recognises. Never the stages' output, the config of the source or its repository.
 */
data class VerificationNotice(
    val tenantId: UUID,
    val verificationId: UUID,
    val sourceName: String,
    val status: VerificationStatus,
    val level: Int,
    val reason: String?,
    /** restic's id of the snapshot. */
    val snapshotId: String,
    val verifierHostname: String?,
    val stages: List<StepOutput.Stage>,
    val mismatches: List<StepOutput.Mismatch>,
    val restoreMillis: Long?,
    /** The source's record before this one that is a pass or a failure; null for none (ОВ11а). */
    val previous: VerificationStatus?,
    /** This pass is the one that closed a «not verified for longer than N days» episode (ОВ11б). */
    val closedStaleEpisode: Boolean,
)

/** What a verification record is told as: nothing for a record that does not turn events (null from [of]). */
enum class VerificationTelling {
    FIRST_FAILURE,
    RECOVERY,
    ;

    companion object {
        /**
         * A failure is told when it opens a series: a verification that did not take place proves neither the backup
         * nor its failure and does not break a series; neither does a cancel by a person, which is never told (J1f,
         * ВJ4, ВJf2). A pass is told when it ends a series of failures or a stale
         * episode, once even if both; successes otherwise are silent.
         */
        fun of(notice: VerificationNotice): VerificationTelling? =
            when (notice.status) {
                VerificationStatus.FAILED -> FIRST_FAILURE.takeUnless { notice.previous == VerificationStatus.FAILED }

                VerificationStatus.PASSED -> RECOVERY.takeIf { turned(notice) }

                VerificationStatus.CANNOT_VERIFY,
                VerificationStatus.INFRASTRUCTURE_FAILURE,
                VerificationStatus.NO_VERIFIER,
                VerificationStatus.CANCELLED,
                -> null
            }

        private fun turned(notice: VerificationNotice): Boolean {
            val afterFailure = notice.previous == VerificationStatus.FAILED
            return afterFailure || notice.closedStaleEpisode
        }
    }
}

/**
 * An open «not verified successfully for longer than N days» episode (V6) as a notification sees it. The times are
 * the cycle's: [timezone] is its zone.
 */
data class StaleNotice(
    val tenantId: UUID,
    val episodeId: UUID,
    val sourceId: UUID,
    val sourceName: String,
    /** The threshold in force when the episode opened. */
    val thresholdDays: Int,
    /** The last pass up to the opening; null for a source never verified. */
    val lastPassAt: Instant?,
    /** The last record up to the opening; null when the source has none. */
    val lastOutcome: VerificationStatus?,
    val timezone: String,
)

/** The installation entered the «no verifier» state (V6, ОВ4): sources to verify and not one verifier. */
data class NoVerifierNotice(
    val tenantId: UUID,
    val episodeId: UUID,
    /** Sources with verification on when the state began. */
    val sources: Int,
)

/** The wording of the messages about restore verification (V6); null from [verification] when a record is silent. */
interface VerificationFormatter {
    fun verification(notice: VerificationNotice): Message?

    fun stale(notice: StaleNotice): Message

    fun noVerifier(notice: NoVerifierNotice): Message
}
