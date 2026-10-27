// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.verify.FingerprintOutcome
import dev.sard.server.verify.FingerprintRow
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** V6: a fingerprint field is shown with the rule of its fact; a rule the console does not know is only information. */
@MutFlowTest
class VerificationConsoleMappingTest {
    private fun ruleShown(rule: String) =
        MutFlow
            .underTest { fieldOf(FingerprintRow("size", rule, "1", null, FingerprintOutcome.NOT_COMPARED)) }
            .rule

    @Test
    fun `an exact fact is shown as exact and any other rule as info`() {
        assertEquals(FingerprintRule.EXACT, ruleShown("exact"))
        assertEquals(FingerprintRule.INFO, ruleShown("info"))
        assertEquals(FingerprintRule.INFO, ruleShown("approximate"))
    }
}
