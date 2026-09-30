// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.results

import dev.sard.proto.agent.v1.StepResult
import dev.sard.proto.agent.v1.StepStatus
import dev.sard.server.runs.StepOutput
import dev.sard.server.runs.StepReport
import dev.sard.server.runs.StepState

/**
 * StepResult → the domain's [StepReport], field by field; whether it fits the step is the
 * domain's check (runs/ResultCheck). uint64 sizes arrive as signed longs: one above the signed
 * range reads negative, and the check refuses it.
 */
object ProtoReports {
    fun of(result: StepResult) = StepReport(status(result.status), result.message, output(result))

    private fun status(status: StepStatus): StepState? =
        when (status) {
            StepStatus.STEP_STATUS_SUCCEEDED -> StepState.SUCCEEDED
            StepStatus.STEP_STATUS_FAILED -> StepState.FAILED
            StepStatus.STEP_STATUS_CANCELLED -> StepState.CANCELLED
            StepStatus.STEP_STATUS_TIMED_OUT -> StepState.TIMED_OUT
            StepStatus.STEP_STATUS_REJECTED -> StepState.REJECTED
            StepStatus.STEP_STATUS_UNSPECIFIED, StepStatus.UNRECOGNIZED -> null
        }

    private fun output(result: StepResult): StepOutput? =
        when (result.outputCase) {
            StepResult.OutputCase.BACKUP -> {
                with(result.backup) { StepOutput.Backup(snapshotId, totalBytes, addedBytes, repositoryId) }
            }

            StepResult.OutputCase.RESTORE -> {
                StepOutput.Restore(result.restore.target)
            }

            StepResult.OutputCase.VERIFY -> {
                StepOutput.Verify(
                    result.verify.snapshotId,
                    result.verify.checksList.map { StepOutput.Check(it.name, it.passed, it.detail) },
                )
            }

            StepResult.OutputCase.RUN -> {
                StepOutput.Run(result.run.exitCode)
            }

            StepResult.OutputCase.OUTPUT_NOT_SET, null -> {
                null
            }
        }
}
