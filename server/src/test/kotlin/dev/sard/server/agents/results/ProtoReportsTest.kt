// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.results

import com.google.protobuf.Duration
import com.google.protobuf.Timestamp
import dev.sard.proto.agent.v1.BackupOutput
import dev.sard.proto.agent.v1.CheckResult
import dev.sard.proto.agent.v1.FingerprintMismatch
import dev.sard.proto.agent.v1.RestoreOutput
import dev.sard.proto.agent.v1.RunOutput
import dev.sard.proto.agent.v1.StageStatus
import dev.sard.proto.agent.v1.StepResult
import dev.sard.proto.agent.v1.StepStatus
import dev.sard.proto.agent.v1.VerifyOutput
import dev.sard.proto.agent.v1.VerifyStage
import dev.sard.proto.agent.v1.VerifyVerdict
import dev.sard.server.runs.StageState
import dev.sard.server.runs.StepOutput
import dev.sard.server.runs.StepReport
import dev.sard.server.runs.StepState
import dev.sard.server.runs.VerifierVerdict
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
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

    @Test
    fun `a verification's stages, verdict, differences and restore time map field by field`() {
        val stage =
            VerifyStage
                .newBuilder()
                .setLevel(2)
                .setName("restore")
                .setStatus(StageStatus.STAGE_STATUS_FAILED)
                .setStartedAt(Timestamp.newBuilder().setSeconds(100))
                .setFinishedAt(Timestamp.newBuilder().setSeconds(160).setNanos(5))
                .setExitCode(1)
                .setOutput("pg_restore: error")
                .setImageDigest("sha256:abc")
        val verify =
            VerifyOutput
                .newBuilder()
                .setSnapshotId("s")
                .addStages(stage)
                .addStages(VerifyStage.newBuilder().setLevel(4).setStatusValue(99))
                .setVerdict(VerifyVerdict.VERIFY_VERDICT_FAILED)
                .addMismatches(
                    FingerprintMismatch
                        .newBuilder()
                        .setName("tables")
                        .setExpected("12")
                        .setActual("0"),
                ).setRestoreDuration(Duration.newBuilder().setSeconds(60).setNanos(5_000_000))

        val output = MutFlow.underTest { ProtoReports.of(builder().setVerify(verify).build()) }.output

        val expected =
            StepOutput.Verify(
                "s",
                emptyList(),
                listOf(
                    StepOutput.Stage(
                        2,
                        "restore",
                        StageState.FAILED,
                        Instant.ofEpochSecond(100),
                        Instant.ofEpochSecond(160, 5),
                        1,
                        "pg_restore: error",
                        "sha256:abc",
                    ),
                    StepOutput.Stage(4, "", null, null, null, 0, "", ""),
                ),
                VerifierVerdict.FAILED,
                listOf(StepOutput.Mismatch("tables", "12", "0")),
                60_005,
            )
        assertEquals(expected, output)
    }

    @Test
    fun `each verdict and stage status maps to its domain value, an unspecified one to none`() {
        val verdicts =
            mapOf(
                VerifyVerdict.VERIFY_VERDICT_PASSED to VerifierVerdict.PASSED,
                VerifyVerdict.VERIFY_VERDICT_FAILED to VerifierVerdict.FAILED,
                VerifyVerdict.VERIFY_VERDICT_CANNOT_VERIFY to VerifierVerdict.CANNOT_VERIFY,
                VerifyVerdict.VERIFY_VERDICT_UNSPECIFIED to null,
            )
        val states =
            mapOf(
                StageStatus.STAGE_STATUS_PASSED to StageState.PASSED,
                StageStatus.STAGE_STATUS_FAILED to StageState.FAILED,
                StageStatus.STAGE_STATUS_CANNOT_RUN to StageState.CANNOT_RUN,
                StageStatus.STAGE_STATUS_NOT_APPLICABLE to StageState.NOT_APPLICABLE,
                StageStatus.STAGE_STATUS_UNSPECIFIED to null,
            )
        for ((verdict, expected) in verdicts) {
            val result = builder().setVerify(VerifyOutput.newBuilder().setVerdict(verdict)).build()
            val output = MutFlow.underTest { ProtoReports.of(result) }.output as StepOutput.Verify
            assertEquals(expected, output.verdict)
            assertEquals(null, output.restoreMillis)
        }
        for ((status, expected) in states) {
            val verify = VerifyOutput.newBuilder().addStages(VerifyStage.newBuilder().setStatus(status))
            val output = MutFlow.underTest { ProtoReports.of(builder().setVerify(verify).build()) }.output
            assertEquals(expected, (output as StepOutput.Verify).stages.single().state)
        }
    }
}
