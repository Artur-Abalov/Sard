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

private const val SOURCE_NAME = "The source's current name; for a deleted source, its name when it was deleted"
private const val MESSAGE = "Why it failed, was rejected or lost; null on success"

@Schema(description = "What a catch-up run stands for: the fires missed while the server was down")
data class CatchUp(
    @field:Schema(description = "The first fire missed")
    val missedFrom: Instant,
    @field:Schema(description = "The last fire missed; with a capped count, the last one counted")
    val missedUntil: Instant,
    @field:Schema(description = "How many fires were missed (counting stops at 10 000)")
    val missedCount: Int,
    @field:Schema(description = "More fires were missed than counted: missedCount is a lower bound")
    val missedCountCapped: Boolean,
    @field:Schema(description = "The schedule's time zone, to show the period in")
    val timezone: String,
)

private const val CATCH_UP = "Set only for trigger catch_up: the downtime it stands for"

@Schema(description = "A run in the list")
data class RunSummary(
    val id: UUID,
    @field:Schema(description = "The source the run backs up")
    val sourceId: UUID,
    @field:Schema(description = SOURCE_NAME)
    val sourceName: String,
    @field:Schema(description = "The source was deleted; its runs and snapshots stay")
    val sourceDeleted: Boolean,
    @field:Schema(description = "The source's agent")
    val agentId: UUID,
    val trigger: RunTrigger,
    val status: RunStatus,
    @field:Schema(description = MESSAGE)
    val message: String?,
    val queuedAt: Instant,
    val startedAt: Instant?,
    val finishedAt: Instant?,
    @field:Schema(description = CATCH_UP)
    val catchUp: CatchUp?,
)

@Schema(description = "What a backup step produced")
data class BackupOutput(
    @field:Schema(description = "restic snapshot id")
    val snapshotId: String,
    val totalBytes: Long,
    val addedBytes: Long,
    @field:Schema(description = "restic repository id; the same key held by several hosts shares it (ADR 0008)")
    val repositoryId: String,
    @field:Schema(
        description =
            "The result that brought this output failed: the snapshot is usable but incomplete, like " +
                "Snapshot.partial",
    )
    val partial: Boolean,
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
    @field:Schema(description = "Files processed so far in the current phase; null until reported")
    val filesProcessed: Long?,
    @field:Schema(description = "Files expected in the current phase; null while unknown")
    val filesTotal: Long?,
    @field:Schema(description = MESSAGE)
    val message: String?,
    @field:Schema(
        description =
            "Set when the step sent a valid backup output, whatever its status: a failed backup that saved a " +
                "snapshot shows it, and a lost step shows a late one",
    )
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
    @field:Schema(description = SOURCE_NAME)
    val sourceName: String,
    @field:Schema(description = "The source was deleted; its runs and snapshots stay")
    val sourceDeleted: Boolean,
    val agentId: UUID,
    val trigger: RunTrigger,
    val status: RunStatus,
    @field:Schema(description = MESSAGE)
    val message: String?,
    val queuedAt: Instant,
    val startedAt: Instant?,
    val finishedAt: Instant?,
    val steps: List<RunStep>,
    @field:Schema(description = CATCH_UP)
    val catchUp: CatchUp?,
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
    @field:Schema(description = "Time on the agent; null for a line the server wrote itself (the truncation mark)")
    val time: Instant?,
    val level: LogLevel,
    @field:Schema(description = "Secret values are redacted by the agent")
    val text: String,
)

@Schema(description = "Log lines after afterSeq, in seq order")
data class LogPage(
    val items: List<LogLine>,
    @field:Schema(description = "Pass as afterSeq to continue: seq of the last line, or afterSeq when there are none")
    val nextAfterSeq: Long,
    @field:Schema(description = "More lines are there already; false is the end for now, poll to follow a step")
    val hasMore: Boolean,
    @field:Schema(description = "The server cut this step's log at its size limit; the last line says so")
    val truncated: Boolean,
)

/** What the run endpoints do; S8b implements it. */
interface RunsApi {
    fun listRuns(
        sourceId: UUID?,
        agentId: UUID?,
        status: List<RunStatus>?,
        queuedFrom: Instant?,
        queuedTo: Instant?,
        cursor: String?,
        limit: Int,
    ): RunPage

    fun getRun(runId: UUID): Run

    fun listStepLogs(
        runId: UUID,
        stepId: UUID,
        afterSeq: Long,
        limit: Int,
    ): LogPage
}

@RestController
@RequestMapping("/api/v1/runs", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "runs")
class RunsController(
    private val api: RunsApi,
) {
    @GetMapping
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "List runs",
        description =
            "Newest first. Filters combine with AND; status values with OR. agentId is the agent that ran " +
                "the step, not the source's current agent.",
    )
    @Unprocessable
    fun listRuns(
        @RequestParam(required = false) sourceId: UUID?,
        @RequestParam(required = false) agentId: UUID?,
        @RequestParam(required = false) status: List<RunStatus>?,
        @Parameter(description = "Queued at or after this time")
        @RequestParam(required = false)
        queuedFrom: Instant?,
        @Parameter(description = "Queued before this time; must not be earlier than queuedFrom")
        @RequestParam(required = false)
        queuedTo: Instant?,
        @PageCursor @RequestParam(required = false) cursor: String?,
        @PageLimit @RequestParam(defaultValue = DEFAULT_LIMIT) limit: Int,
    ): RunPage = api.listRuns(sourceId, agentId, status, queuedFrom, queuedTo, cursor, limit)

    @GetMapping("/{runId}")
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "Run card with steps")
    @NotFound
    fun getRun(
        @PathVariable runId: UUID,
    ): Run = api.getRun(runId)

    @GetMapping("/{runId}/steps/{stepId}/logs")
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "Log of a step", description = "Lines with seq > afterSeq, oldest first.")
    @NotFound
    @Unprocessable
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
    ): LogPage = api.listStepLogs(runId, stepId, afterSeq, limit)
}
