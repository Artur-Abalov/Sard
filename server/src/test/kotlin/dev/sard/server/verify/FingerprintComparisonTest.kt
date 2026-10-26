// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.runs.FingerprintFact
import dev.sard.server.runs.StageState
import dev.sard.server.runs.StepOutput
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

private val FACTS =
    listOf(
        FingerprintFact("tables", "exact", "42"),
        FingerprintFact("rows:public.orders", "exact", "1204311"),
        FingerprintFact("database_size", "info", "900"),
    )

private fun stage(
    name: String,
    state: StageState,
) = StepOutput.Stage(4, name, state, null, null, 0, "", "")

/** V6 (ОВ7): the fingerprint of the source and of the copy side by side, field by field. */
@MutFlowTest
class FingerprintComparisonTest {
    private fun rows(
        mismatches: List<StepOutput.Mismatch>,
        compared: Boolean = true,
        facts: List<FingerprintFact> = FACTS,
    ) = MutFlow.underTest { FingerprintComparison.rows(facts, mismatches, compared) }

    @Test
    fun `a matched exact fact shows the source value as the copy, a mismatched one the copy's, an info fact none`() {
        val rows = rows(listOf(StepOutput.Mismatch("rows:public.orders", "1204311", "1204310")))

        assertEquals(
            listOf(
                FingerprintRow("tables", "exact", "42", "42", FingerprintOutcome.MATCHED),
                FingerprintRow("rows:public.orders", "exact", "1204311", "1204310", FingerprintOutcome.MISMATCHED),
                FingerprintRow("database_size", "info", "900", null, FingerprintOutcome.NOT_COMPARED),
            ),
            rows,
        )
    }

    @Test
    fun `an info fact the verifier names as a mismatch is a mismatch`() {
        val rows = rows(listOf(StepOutput.Mismatch("database_size", "900", "800")), facts = FACTS.takeLast(1))

        assertEquals(listOf(FingerprintRow("database_size", "info", "900", "800", FingerprintOutcome.MISMATCHED)), rows)
    }

    @Test
    fun `facts the copy was not compared against are not compared`() {
        val rows = rows(emptyList(), compared = false)

        assertEquals(List(3) { FingerprintOutcome.NOT_COMPARED }, rows.map { it.outcome })
        assertEquals(listOf(null, null, null), rows.map { it.copy })
    }

    @Test
    fun `the copy was compared when the verification passed, mismatched, or its fingerprint stage ran`() {
        fun compared(
            status: VerificationStatus,
            mismatches: List<StepOutput.Mismatch>,
            vararg stages: StepOutput.Stage,
        ) = MutFlow.underTest { FingerprintComparison.compared(status, mismatches, stages.toList()) }
        val mismatch = listOf(StepOutput.Mismatch("tables", "1", "2"))

        assertEquals(true, compared(VerificationStatus.PASSED, emptyList()))
        assertEquals(true, compared(VerificationStatus.FAILED, mismatch))
        assertEquals(true, compared(VerificationStatus.FAILED, emptyList(), stage("fingerprint", StageState.FAILED)))
        assertEquals(true, compared(VerificationStatus.FAILED, emptyList(), stage("fingerprint", StageState.PASSED)))
        assertEquals(false, compared(VerificationStatus.FAILED, emptyList(), stage("restic_check", StageState.FAILED)))
        assertEquals(
            false,
            compared(VerificationStatus.CANNOT_VERIFY, emptyList(), stage("fingerprint", StageState.CANNOT_RUN)),
        )
        assertEquals(false, compared(VerificationStatus.NO_VERIFIER, emptyList()))
    }
}
