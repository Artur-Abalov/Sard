// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

private val EPISODE = UUID(0, 7)

/** V6 (ОВ4, ОВ11в): an episode of the «no verifier» state opens when it begins and closes when it ends. */
@MutFlowTest
class NoVerifierStateTest {
    private fun judge(
        sources: Int,
        anyVerifier: Boolean,
        open: UUID? = null,
    ) = MutFlow.underTest { NoVerifierState.judge(sources, anyVerifier, open) }

    @Test
    fun `sources to verify and no verifier open an episode that counts them`() {
        assertEquals(NoVerifierTurn.Open(3), judge(3, anyVerifier = false))
    }

    @Test
    fun `the state going on keeps its episode, no second one`() {
        assertEquals(NoVerifierTurn.Keep, judge(3, anyVerifier = false, open = EPISODE))
    }

    @Test
    fun `a verifier closes the episode, silently`() {
        assertEquals(NoVerifierTurn.Close(EPISODE), judge(3, anyVerifier = true, open = EPISODE))
    }

    @Test
    fun `nothing to verify is no state, even without a verifier, and closes an open episode`() {
        assertEquals(NoVerifierTurn.Keep, judge(0, anyVerifier = false))
        assertEquals(NoVerifierTurn.Close(EPISODE), judge(0, anyVerifier = false, open = EPISODE))
    }

    @Test
    fun `no state and no episode is nothing to do`() {
        assertEquals(NoVerifierTurn.Keep, judge(3, anyVerifier = true))
    }
}
