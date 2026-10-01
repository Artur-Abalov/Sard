// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.results

import dev.sard.proto.agent.v1.LogChunk
import dev.sard.proto.agent.v1.LogLevel
import dev.sard.proto.agent.v1.StepPhase
import dev.sard.proto.agent.v1.StepProgress
import dev.sard.proto.agent.v1.StepResult
import dev.sard.proto.agent.v1.StepStatus
import dev.sard.server.runs.LogLine
import dev.sard.server.runs.ProgressReport
import dev.sard.server.runs.StepOutput
import dev.sard.server.runs.StepReport
import dev.sard.server.runs.StepState
import java.time.Instant

/**
 * StepResult → the domain's [StepReport], field by field; whether it fits the step is the
 * domain's check (runs/ResultCheck). uint64 sizes arrive as signed longs: one above the signed
 * range reads negative, and the check refuses it.
 */
object ProtoReports {
    fun of(result: StepResult) = StepReport(status(result.status), result.message, output(result))

    /**
     * A phase by its name (the schema's CHECK list); a total of 0 is unknown (proto), a negative counter too. Files
     * processed 0 with no total means the phase counts no files or restic has reported none yet: unknown.
     */
    fun progress(progress: StepProgress) =
        ProgressReport(
            phase(progress.phase),
            progress.bytesProcessed.takeIf { it >= 0 },
            progress.bytesTotal.takeIf { it > 0 },
            progress.filesProcessed.takeIf { it >= 0 && (it > 0 || progress.filesTotal > 0) },
            progress.filesTotal.takeIf { it > 0 },
        )

    /** Lines in the chunk's order; a line without a time keeps none, one without a level is info. */
    fun lines(chunk: LogChunk) =
        chunk.linesList.map { line ->
            val time = if (line.hasTime()) Instant.ofEpochSecond(line.time.seconds, line.time.nanos.toLong()) else null
            LogLine(time, level(line.level), line.text)
        }

    /** A phase by the name the schema stores (`accepted`, …, `verifying`). */
    private fun phase(phase: StepPhase): String? =
        if (phase == StepPhase.STEP_PHASE_UNSPECIFIED || phase == StepPhase.UNRECOGNIZED) {
            null
        } else {
            phase.name.removePrefix("STEP_PHASE_").lowercase()
        }

    private fun level(level: LogLevel): String =
        when (level) {
            LogLevel.LOG_LEVEL_DEBUG -> "debug"
            LogLevel.LOG_LEVEL_INFO, LogLevel.LOG_LEVEL_UNSPECIFIED, LogLevel.UNRECOGNIZED -> "info"
            LogLevel.LOG_LEVEL_WARN -> "warn"
            LogLevel.LOG_LEVEL_ERROR -> "error"
        }

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
