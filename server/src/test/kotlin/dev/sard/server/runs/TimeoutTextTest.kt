// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** A step timeout as the server's messages say it: the biggest unit that holds it whole. */
@MutFlowTest
class TimeoutTextTest {
    @Test
    fun `a timeout is told in whole hours, else whole minutes, else seconds`() {
        assertEquals("6 h", MutFlow.underTest { timeoutText(21_600) })
        assertEquals("24 h", MutFlow.underTest { timeoutText(86_400) })
        assertEquals("90 min", MutFlow.underTest { timeoutText(5_400) })
        assertEquals("1 min", MutFlow.underTest { timeoutText(60) })
        assertEquals("59 s", MutFlow.underTest { timeoutText(59) })
        assertEquals("61 s", MutFlow.underTest { timeoutText(61) })
    }
}
