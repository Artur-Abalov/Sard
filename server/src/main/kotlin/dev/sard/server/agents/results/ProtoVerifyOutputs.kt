// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.results

import com.google.protobuf.Duration
import com.google.protobuf.Timestamp
import dev.sard.proto.agent.v1.StageStatus
import dev.sard.proto.agent.v1.VerifyOutput
import dev.sard.proto.agent.v1.VerifyStage
import dev.sard.proto.agent.v1.VerifyVerdict
import dev.sard.server.runs.StageState
import dev.sard.server.runs.StepOutput
import dev.sard.server.runs.VerifierVerdict
import java.time.Instant

/** VerifyOutput → the domain's [StepOutput.Verify] (V4), field by field; unknown enum values are null. */
internal object ProtoVerifyOutputs {
    /** A verification's evidence; a stage time or restore time the agent did not set is none. */
    fun of(verify: VerifyOutput) =
        StepOutput.Verify(
            verify.snapshotId,
            verify.checksList.map { StepOutput.Check(it.name, it.passed, it.detail) },
            verify.stagesList.map(::stage),
            verdict(verify.verdict),
            verify.mismatchesList.map { StepOutput.Mismatch(it.name, it.expected, it.actual) },
            if (verify.hasRestoreDuration()) millis(verify.restoreDuration) else null,
        )

    private fun stage(stage: VerifyStage) =
        StepOutput.Stage(
            stage.level,
            stage.name,
            stageState(stage.status),
            if (stage.hasStartedAt()) instant(stage.startedAt) else null,
            if (stage.hasFinishedAt()) instant(stage.finishedAt) else null,
            stage.exitCode,
            stage.output,
            stage.imageDigest,
        )

    fun instant(time: Timestamp): Instant = Instant.ofEpochSecond(time.seconds, time.nanos.toLong())

    private fun millis(duration: Duration): Long =
        java.time.Duration
            .ofSeconds(duration.seconds, duration.nanos.toLong())
            .toMillis()

    private fun verdict(verdict: VerifyVerdict): VerifierVerdict? =
        when (verdict) {
            VerifyVerdict.VERIFY_VERDICT_PASSED -> VerifierVerdict.PASSED
            VerifyVerdict.VERIFY_VERDICT_FAILED -> VerifierVerdict.FAILED
            VerifyVerdict.VERIFY_VERDICT_CANNOT_VERIFY -> VerifierVerdict.CANNOT_VERIFY
            VerifyVerdict.VERIFY_VERDICT_INFRASTRUCTURE_FAILURE -> VerifierVerdict.INFRASTRUCTURE_FAILURE
            VerifyVerdict.VERIFY_VERDICT_UNSPECIFIED, VerifyVerdict.UNRECOGNIZED -> null
        }

    private fun stageState(status: StageStatus): StageState? =
        when (status) {
            StageStatus.STAGE_STATUS_PASSED -> StageState.PASSED
            StageStatus.STAGE_STATUS_FAILED -> StageState.FAILED
            StageStatus.STAGE_STATUS_CANNOT_RUN -> StageState.CANNOT_RUN
            StageStatus.STAGE_STATUS_NOT_APPLICABLE -> StageState.NOT_APPLICABLE
            StageStatus.STAGE_STATUS_UNSPECIFIED, StageStatus.UNRECOGNIZED -> null
        }
}
