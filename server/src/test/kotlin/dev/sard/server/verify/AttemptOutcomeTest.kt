// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.runs.StageState
import dev.sard.server.runs.StepOutput
import dev.sard.server.runs.StepState
import dev.sard.server.runs.VerifierVerdict
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs

private fun stage(
    level: Int,
    state: StageState = StageState.PASSED,
) = StepOutput.Stage(level, "stage-$level", state, null, null, 0, "", "")

/** The PostgreSQL pipeline of the design document: tag check (0), check (1), restore (2, 3), fingerprint (4). */
private val ALL_PASSED = listOf(stage(0), stage(1), stage(2), stage(3), stage(4), stage(0))

private fun output(
    verdict: VerifierVerdict?,
    stages: List<StepOutput.Stage> = ALL_PASSED,
    restoreMillis: Long? = 90_000,
) = StepOutput.Verify("snap", emptyList(), stages, verdict, emptyList(), restoreMillis)

/** V-D2 and V-D10: the level a pipeline reached, and what an attempt's end means for the job. */
@MutFlowTest
class AttemptOutcomeTest {
    private fun outcome(
        status: StepState,
        output: StepOutput.Verify?,
        failedBefore: Int = 0,
    ) = MutFlow.underTest { AttemptOutcome.of(status, "agent message", output, failedBefore) }

    @Test
    fun `every level passed and the restore time reported reach level 5`() {
        assertEquals(5, MutFlow.underTest { Levels.reached(ALL_PASSED, restoreMillis = 1) })
    }

    @Test
    fun `without the restore time the pipeline stops at level 4`() {
        assertEquals(4, MutFlow.underTest { Levels.reached(ALL_PASSED, restoreMillis = null) })
    }

    @Test
    fun `the level is the highest one below the first stage that did not pass`() {
        val stages = listOf(stage(0), stage(1), stage(2), stage(3, StageState.FAILED))

        assertEquals(2, MutFlow.underTest { Levels.reached(stages, restoreMillis = 1) })
    }

    @Test
    fun `a level that does not apply counts as reached, as level 3 of files does`() {
        val stages = listOf(stage(1), stage(2), stage(3, StageState.NOT_APPLICABLE), stage(4))

        assertEquals(5, MutFlow.underTest { Levels.reached(stages, restoreMillis = 1) })
    }

    @Test
    fun `a level with no stage is not reached`() {
        assertEquals(1, MutFlow.underTest { Levels.reached(listOf(stage(1), stage(3), stage(4)), restoreMillis = 1) })
    }

    @Test
    fun `a failed preparation stage reaches no level at all`() {
        val stages = listOf(stage(0, StageState.FAILED), stage(1), stage(2))

        assertEquals(0, MutFlow.underTest { Levels.reached(stages, restoreMillis = 1) })
    }

    @Test
    fun `a stage that could not run stops the levels as a failure does`() {
        val stages = listOf(stage(1), stage(2, StageState.CANNOT_RUN), stage(3))

        assertEquals(1, MutFlow.underTest { Levels.reached(stages, restoreMillis = 1) })
    }

    @Test
    fun `a passed verdict of a succeeded step is a passed verification with its level`() {
        val settled = assertIs<AttemptOutcome.Settled>(outcome(StepState.SUCCEEDED, output(VerifierVerdict.PASSED)))

        assertEquals(VerificationStatus.PASSED, settled.status)
        assertEquals(5, settled.level)
    }

    @Test
    fun `a failed verdict is a failed verification, the backup is bad`() {
        val stages = listOf(stage(1), stage(2, StageState.FAILED))

        val failed = outcome(StepState.FAILED, output(VerifierVerdict.FAILED, stages))

        val settled = assertIs<AttemptOutcome.Settled>(failed)

        assertEquals(VerificationStatus.FAILED, settled.status)
        assertEquals(1, settled.level)
    }

    @Test
    fun `a verdict that it cannot verify is not a failure of the backup`() {
        val settled = assertIs<AttemptOutcome.Settled>(outcome(StepState.FAILED, output(VerifierVerdict.CANNOT_VERIFY)))

        assertEquals(VerificationStatus.CANNOT_VERIFY, settled.status)
    }

    @Test
    fun `a result without a verdict or without output cannot verify, whatever the step says`() {
        for (verify in listOf(output(verdict = null), null)) {
            val settled = assertIs<AttemptOutcome.Settled>(outcome(StepState.SUCCEEDED, verify))
            assertEquals(VerificationStatus.CANNOT_VERIFY, settled.status)
            assertEquals("no verdict: agent message", settled.reason)
        }
    }

    @Test
    fun `a passed verdict below level 4 or of a failed step is not believed`() {
        val short = output(VerifierVerdict.PASSED, listOf(stage(1), stage(2)))

        val cases = listOf(StepState.SUCCEEDED to short, StepState.FAILED to output(VerifierVerdict.PASSED))
        for ((status, verify) in cases) {
            val settled = assertIs<AttemptOutcome.Settled>(outcome(status, verify))
            assertEquals(VerificationStatus.CANNOT_VERIFY, settled.status)
            assertEquals("a passed verdict that its stages do not prove", settled.reason)
        }
    }

    @Test
    fun `a passed verdict at level 4 exactly is a pass, at level 3 it is not believed`() {
        val toThree = listOf(stage(1), stage(2), stage(3))
        val atFour = output(VerifierVerdict.PASSED, toThree + stage(4), restoreMillis = null)
        val atThree = output(VerifierVerdict.PASSED, toThree, restoreMillis = null)

        val four = assertIs<AttemptOutcome.Settled>(outcome(StepState.SUCCEEDED, atFour))
        val three = assertIs<AttemptOutcome.Settled>(outcome(StepState.SUCCEEDED, atThree))

        val statuses = listOf(four.status, three.status)
        assertEquals(listOf(VerificationStatus.PASSED, VerificationStatus.CANNOT_VERIFY), statuses)
        assertEquals(listOf(4, 3), listOf(four.level, three.level))
    }

    @Test
    fun `a lost, rejected, timed out or cancelled attempt is tried again`() {
        for (status in listOf(StepState.LOST, StepState.REJECTED, StepState.TIMED_OUT, StepState.CANCELLED)) {
            assertEquals(AttemptOutcome.Retry(failedAttempts = 2), outcome(status, null, failedBefore = 1))
        }
    }

    @Test
    fun `the third attempt in a row without a verdict is an infrastructure failure`() {
        val settled = assertIs<AttemptOutcome.Settled>(outcome(StepState.LOST, null, failedBefore = 2))

        assertEquals(VerificationStatus.INFRASTRUCTURE_FAILURE, settled.status)
        assertEquals(0, settled.level)
        assertEquals("3 attempts without a verdict; the last: lost: agent message", settled.reason)
    }

    // V5: a stage that ran out of its time means the verification did not take place, as a lost attempt does.
    @Test
    fun `an infrastructure failure from the verifier is tried again, whatever the step says`() {
        val timedOut = output(VerifierVerdict.INFRASTRUCTURE_FAILURE, listOf(stage(0), stage(1, StageState.CANNOT_RUN)))

        for (status in listOf(StepState.SUCCEEDED, StepState.FAILED)) {
            assertEquals(AttemptOutcome.Retry(failedAttempts = 2), outcome(status, timedOut, failedBefore = 1))
        }
    }

    @Test
    fun `the third infrastructure failure in a row ends the job naming the stage that ran out of time`() {
        val timedOut = output(VerifierVerdict.INFRASTRUCTURE_FAILURE, listOf(stage(0), stage(1, StageState.CANNOT_RUN)))

        val settled = assertIs<AttemptOutcome.Settled>(outcome(StepState.FAILED, timedOut, failedBefore = 2))

        assertEquals(VerificationStatus.INFRASTRUCTURE_FAILURE, settled.status)
        assertEquals(0, settled.level)
        val reason = "3 attempts without a verdict; the last: stage stage-1 ran out of time: agent message"
        assertEquals(reason, settled.reason)
    }
}
