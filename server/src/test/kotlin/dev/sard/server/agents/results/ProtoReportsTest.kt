// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.results

import dev.sard.proto.agent.v1.BackupOutput
import dev.sard.proto.agent.v1.CheckResult
import dev.sard.proto.agent.v1.RestoreOutput
import dev.sard.proto.agent.v1.RunOutput
import dev.sard.proto.agent.v1.StepResult
import dev.sard.proto.agent.v1.StepStatus
import dev.sard.proto.agent.v1.VerifyOutput
import dev.sard.server.runs.StepOutput
import dev.sard.server.runs.StepReport
import dev.sard.server.runs.StepState
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** StepResult → the domain's StepReport; the check against the action is the domain's (ResultCheck). */
@MutFlowTest
class ProtoReportsTest {
    private fun builder(status: StepStatus = StepStatus.STEP_STATUS_SUCCEEDED) =
        StepResult
            .newBuilder()
            .setCommandId("c")
            .setStatus(status)
            .setMessage("m")

    @Test
    fun `each final status maps to the step state of the same name`() {
        val expected =
            mapOf(
                StepStatus.STEP_STATUS_SUCCEEDED to StepState.SUCCEEDED,
                StepStatus.STEP_STATUS_FAILED to StepState.FAILED,
                StepStatus.STEP_STATUS_CANCELLED to StepState.CANCELLED,
                StepStatus.STEP_STATUS_TIMED_OUT to StepState.TIMED_OUT,
                StepStatus.STEP_STATUS_REJECTED to StepState.REJECTED,
            )
        for ((status, state) in expected) {
            assertEquals(StepReport(state, "m", null), MutFlow.underTest { ProtoReports.of(builder(status).build()) })
        }
    }

    @Test
    fun `an unspecified or unknown status is no status`() {
        val unknown = builder().setStatusValue(99).build()
        for (result in listOf(builder(StepStatus.STEP_STATUS_UNSPECIFIED).build(), unknown)) {
            assertEquals(StepReport(null, "m", null), MutFlow.underTest { ProtoReports.of(result) })
        }
    }

    @Test
    fun `each output maps to its domain kind`() {
        val backup =
            BackupOutput
                .newBuilder()
                .setSnapshotId("s")
                .setTotalBytes(10)
                .setAddedBytes(2)
                .setRepositoryId("r")
        val check =
            CheckResult
                .newBuilder()
                .setName("n")
                .setPassed(true)
                .setDetail("d")
        val cases =
            mapOf(
                builder().setBackup(backup).build() to StepOutput.Backup("s", 10, 2, "r"),
                builder().setRestore(RestoreOutput.newBuilder().setTarget("/t")).build() to StepOutput.Restore("/t"),
                builder().setVerify(VerifyOutput.newBuilder().setSnapshotId("s").addChecks(check)).build() to
                    StepOutput.Verify("s", listOf(StepOutput.Check("n", true, "d"))),
                builder().setRun(RunOutput.newBuilder().setExitCode(3)).build() to StepOutput.Run(3),
            )
        for ((result, output) in cases) {
            assertEquals(output, MutFlow.underTest { ProtoReports.of(result) }.output)
        }
    }

    @Test
    fun `sizes above the signed range stay negative, so the domain check refuses them`() {
        val backup =
            BackupOutput
                .newBuilder()
                .setSnapshotId("s")
                .setTotalBytes(-1)
                .setRepositoryId("r")

        val output = MutFlow.underTest { ProtoReports.of(builder().setBackup(backup).build()) }.output

        assertEquals(StepOutput.Backup("s", -1, 0, "r"), output)
    }
}
