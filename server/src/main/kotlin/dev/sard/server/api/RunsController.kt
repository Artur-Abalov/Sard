// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.Parameter
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.tags.Tag
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PathVariable
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RequestParam
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import java.time.Instant
import java.util.UUID

private const val MESSAGE = "Why it failed, was rejected or lost; null on success"

@Schema(description = "A run in the list")
data class RunSummary(
    val id: UUID,
    @field:Schema(description = "The source the run backs up")
    val sourceId: UUID,
    @field:Schema(description = "The source's agent")
    val agentId: UUID,
    val trigger: RunTrigger,
    val status: RunStatus,
    @field:Schema(description = MESSAGE)
    val message: String?,
    val queuedAt: Instant,
    val startedAt: Instant?,
    val finishedAt: Instant?,
)

@Schema(description = "What a backup step produced")
data class BackupOutput(
    @field:Schema(description = "restic snapshot id")
    val snapshotId: String,
    val totalBytes: Long,
    val addedBytes: Long,
)

@Schema(description = "One command to the agent")
data class RunStep(
    @field:Schema(description = "Also the command_id of the agent protocol")
    val id: UUID,
    val ordinal: Int,
    val action: StepAction,
    val status: StepStatus,
    @field:Schema(description = "Last reported phase; null before the agent accepts the step")
    val phase: StepPhase?,
    val agentId: UUID,
    @field:Schema(description = "null for action run")
    val sourceId: UUID?,
    val plugin: String,
    @field:Schema(description = "null for action run")
    val repositoryName: String?,
    val bytesProcessed: Long?,
    @field:Schema(description = "null while unknown")
    val bytesTotal: Long?,
    @field:Schema(description = MESSAGE)
    val message: String?,
    @field:Schema(description = "Set when a backup step succeeded")
    val backup: BackupOutput?,
    val queuedAt: Instant,
    val dispatchedAt: Instant?,
    val startedAt: Instant?,
    val finishedAt: Instant?,
)

@Schema(description = "A run with its steps")
data class Run(
    val id: UUID,
    val sourceId: UUID,
    val agentId: UUID,
    val trigger: RunTrigger,
    val status: RunStatus,
    @field:Schema(description = MESSAGE)
    val message: String?,
    val queuedAt: Instant,
    val startedAt: Instant?,
    val finishedAt: Instant?,
    val steps: List<RunStep>,
)

@Schema(description = "A page of runs")
data class RunPage(
    val items: List<RunSummary>,
    @field:Schema(description = CURSOR_NEXT)
    val nextCursor: String?,
)

@Schema(description = "A line of a step's log")
data class LogLine(
    @field:Schema(description = "Position in the step's log, assigned by the server; increasing, starts at 1")
    val seq: Long,
    @field:Schema(description = "Time on the agent")
    val time: Instant,
    val level: LogLevel,
    @field:Schema(description = "Secret values are redacted by the agent")
    val text: String,
)

@Schema(description = "Log lines after afterSeq, in seq order")
data class LogPage(
    val items: List<LogLine>,
    @field:Schema(description = "Pass as afterSeq to continue: seq of the last line, or afterSeq when there are none")
    val nextAfterSeq: Long,
    @field:Schema(description = "More lines are there already; false means the end for now, poll to follow a running step")
    val hasMore: Boolean,
)

@RestController
@RequestMapping("/api/v1/runs", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "runs")
class RunsController {
    @GetMapping
    @Operation(summary = "List runs", description = "Newest first. Filters combine with AND; status values with OR.")
    fun listRuns(
        @RequestParam(required = false) sourceId: UUID?,
        @RequestParam(required = false) agentId: UUID?,
        @RequestParam(required = false) status: List<RunStatus>?,
        @PageCursor @RequestParam(required = false) cursor: String?,
        @PageLimit @RequestParam(defaultValue = DEFAULT_LIMIT) limit: Int,
    ): RunPage = notImplemented()

    @GetMapping("/{runId}")
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "Run card with steps")
    @NotFound
    fun getRun(
        @PathVariable runId: UUID,
    ): Run = notImplemented()

    @GetMapping("/{runId}/steps/{stepId}/logs")
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "Log of a step", description = "Lines with seq > afterSeq, oldest first.")
    @NotFound
    fun listStepLogs(
        @PathVariable runId: UUID,
        @PathVariable stepId: UUID,
        @Parameter(description = "Return lines after this seq; 0 for the start")
        @RequestParam(defaultValue = "0")
        afterSeq: Long,
        @Parameter(
            description = "Page size",
            schema = Schema(type = "integer", format = "int32", minimum = "1", maximum = "1000", defaultValue = "500"),
        )
        @RequestParam(defaultValue = "500")
        limit: Int,
    ): LogPage = notImplemented()
}
