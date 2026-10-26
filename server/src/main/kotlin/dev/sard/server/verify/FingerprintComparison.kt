// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.runs.FingerprintFact
import dev.sard.server.runs.SourceFingerprint
import dev.sard.server.runs.StageState
import dev.sard.server.runs.StepOutput

/** How a fact of the source's fingerprint stands against the restored copy. */
enum class FingerprintOutcome {
    MATCHED,
    MISMATCHED,
    NOT_COMPARED,
}

/** One field of the fingerprint: the source's [source] value and the copy's [copy] one, null when not known. */
data class FingerprintRow(
    val name: String,
    val rule: String,
    val source: String,
    val copy: String?,
    val outcome: FingerprintOutcome,
)

/** The fingerprint of a verification as the console shows it: its fields, and how many facts are only counted. */
data class FingerprintView(
    val rows: List<FingerprintRow>,
    val hidden: Int,
) {
    companion object {
        val NONE = FingerprintView(emptyList(), 0)
    }
}

/**
 * The source's fingerprint and the copy's side by side (V6, ОВ7). The verifier reports only the facts that differ,
 * so the copy's value is known for a mismatch; a matched exact fact has the source's value by definition, and an info
 * fact is shown but not compared.
 */
object FingerprintComparison {
    private const val FINGERPRINT_STAGE = "fingerprint"
    private const val EXACT = "exact"
    private val RAN = setOf(StageState.PASSED, StageState.FAILED)

    /** The view of the fingerprint a snapshot kept ([bytes] and [sha256]; null for none) and of the verification. */
    fun view(
        bytes: ByteArray?,
        sha256: String?,
        status: VerificationStatus,
        mismatches: List<StepOutput.Mismatch>,
        stages: List<StepOutput.Stage>,
    ): FingerprintView {
        val kept = bytes?.let { SourceFingerprint.of(it, sha256.orEmpty()) } ?: return FingerprintView.NONE
        val summary = kept.summary()
        return FingerprintView(rows(summary.facts, mismatches, compared(status, mismatches, stages)), summary.hidden)
    }

    fun rows(
        facts: List<FingerprintFact>,
        mismatches: List<StepOutput.Mismatch>,
        compared: Boolean,
    ): List<FingerprintRow> {
        val differing = mismatches.associateBy { it.name }
        return facts.map { fact -> row(fact, differing[fact.name], compared) }
    }

    /** Whether the copy was held against the fingerprint: it passed, differed, or its comparison stage ran. */
    fun compared(
        status: VerificationStatus,
        mismatches: List<StepOutput.Mismatch>,
        stages: List<StepOutput.Stage>,
    ): Boolean =
        status == VerificationStatus.PASSED ||
            mismatches.isNotEmpty() ||
            stages.any { it.name == FINGERPRINT_STAGE && it.state in RAN }

    private fun row(
        fact: FingerprintFact,
        mismatch: StepOutput.Mismatch?,
        compared: Boolean,
    ): FingerprintRow =
        when {
            mismatch != null -> of(fact, mismatch.actual, FingerprintOutcome.MISMATCHED)
            compared && fact.rule == EXACT -> of(fact, fact.value, FingerprintOutcome.MATCHED)
            else -> of(fact, null, FingerprintOutcome.NOT_COMPARED)
        }

    private fun of(
        fact: FingerprintFact,
        copy: String?,
        outcome: FingerprintOutcome,
    ) = FingerprintRow(fact.name, fact.rule, fact.value, copy, outcome)
}
