// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.runs.StageState
import dev.sard.server.runs.StepOutput
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private val T: Instant = Instant.parse("2026-10-26T04:30:00Z")

private fun stage(
    start: Long?,
    finish: Long?,
): StepOutput.Stage {
    val started = start?.let { T.plusSeconds(it) }
    val finished = finish?.let { T.plusSeconds(it) }
    return StepOutput.Stage(1, "s", StageState.PASSED, started, finished, 0, "", "")
}

/** V6: how long a verification took, from the times of its stages. */
@MutFlowTest
class ReportDurationTest {
    private fun millis(vararg stages: StepOutput.Stage) = MutFlow.underTest { ReportDuration.millis(stages.toList()) }

    @Test
    fun `the duration runs from the first start to the last finish`() {
        assertEquals(100_000L, millis(stage(5, 20), stage(20, 100), stage(0, 30)))
    }

    @Test
    fun `stages without times give no duration`() {
        assertNull(millis())
        assertNull(millis(stage(null, null)))
        assertNull(millis(stage(1, null), stage(2, null)))
        assertNull(millis(stage(null, 4)))
    }

    @Test
    fun `a stage that finished without a recorded start still bounds the end`() {
        assertEquals(10_000L, millis(stage(0, 4), stage(null, 10)))
        assertEquals(4_000L, millis(stage(1, null), stage(null, 5)))
    }
}
