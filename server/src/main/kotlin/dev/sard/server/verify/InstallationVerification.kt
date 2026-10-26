// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import java.time.Duration
import java.time.Instant
import java.util.UUID

/** A live source and its verification state as the console shows it. */
data class SourceStanding(
    val sourceId: UUID,
    val name: String,
    val summary: VerificationSummary,
)

/**
 * The verification figures of an installation for the overview (V6): how many sources were verified lately, failed,
 * were not held, went stale, were never verified or are not verified on purpose, and the ones to look at first.
 * Sources with verification off count only as [disabled]: neither a failure nor a silence.
 */
data class InstallationVerification(
    val enabled: Int,
    val verifiedLastDay: Int,
    val failed: Int,
    /** Verification did not take place: cannot verify, an infrastructure failure, no fitting verifier. */
    val notHeld: Int,
    /** Sources with an open «not verified for longer than N days» episode. */
    val stale: Int,
    val neverVerified: Int,
    val disabled: Int,
    /** There are sources to verify and not one live verifier (ОВ4). */
    val noVerifier: Boolean,
    /** At most [ATTENTION] sources with verification on, the worst first (ОВ10). */
    val attention: List<SourceStanding>,
) {
    companion object {
        const val ATTENTION = 10
        private val LAST_DAY: Duration = Duration.ofDays(1)
        private val DID_NOT_TAKE_PLACE =
            setOf(
                VerificationStatus.CANNOT_VERIFY,
                VerificationStatus.INFRASTRUCTURE_FAILURE,
                VerificationStatus.NO_VERIFIER,
            )

        fun of(
            standings: List<SourceStanding>,
            now: Instant,
            anyVerifier: Boolean,
        ): InstallationVerification {
            val (verified, off) = standings.partition { it.summary.enabled }
            val summaries = verified.map { it.summary }
            return InstallationVerification(
                enabled = verified.size,
                verifiedLastDay = summaries.count { passedSince(it, now - LAST_DAY) },
                failed = summaries.count { it.status == VerificationStatus.FAILED },
                notHeld = summaries.count { it.status in DID_NOT_TAKE_PLACE },
                stale = summaries.count { it.staleSince != null },
                neverVerified = summaries.count { it.status == null },
                disabled = off.size,
                noVerifier = verified.isNotEmpty() && !anyVerifier,
                attention = verified.sortedWith(WORST_FIRST).take(ATTENTION),
            )
        }

        private fun passedSince(
            summary: VerificationSummary,
            since: Instant,
        ): Boolean = summary.lastVerifiedAt?.let { !it.isBefore(since) } ?: false

        /** failed, not held, stale, never verified, the rest; within a kind the oldest pass first, then the name. */
        private val WORST_FIRST =
            compareBy<SourceStanding> { Kind.of(it.summary).ordinal }
                .thenBy(nullsFirst()) { it.summary.lastVerifiedAt }
                .thenBy { it.name }
    }

    /** What a source is for the overview, the worst first. */
    private enum class Kind {
        FAILED,
        NOT_HELD,
        STALE,
        NEVER_VERIFIED,
        VERIFIED,
        ;

        companion object {
            fun of(summary: VerificationSummary): Kind =
                when {
                    summary.status == VerificationStatus.FAILED -> FAILED
                    summary.status in DID_NOT_TAKE_PLACE -> NOT_HELD
                    summary.staleSince != null -> STALE
                    summary.status == null -> NEVER_VERIFIED
                    else -> VERIFIED
                }
        }
    }
}
