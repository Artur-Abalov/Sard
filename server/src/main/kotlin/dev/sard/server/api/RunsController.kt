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
import org.springframework.web.bind.annotation.PostMapping
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

private const val OUTCOME =
    "What the run came to in words a person reads: the status, told apart by the step (timed_out, rejected, lost), " +
        "the cancel (cancelling, cancelled, cancel_unconfirmed) and the agent (waiting_for_agent: queued while the " +
        "agent is offline; agent_unavailable: not run, the agent was offline until the wait deadline)"

@Schema(description = "A cancel somebody asked for")
data class Cancellation(
    val requestedAt: Instant,
    @field:Schema(description = "\"admin\" for any session of the open core, which has one administrator")
    val requestedBy: String,
    @field:Schema(
        description =
            "pending while the run is not final; cancelled: the agent confirmed (or the run never left the queue); " +
                "unconfirmed: the agent did not confirm within the lost window; finished_first: the agent's result " +
                "came first and is the run's result",
    )
    val result: CancelResult,
)

private const val AGENT_WAIT_UNTIL =
    "Until when the queued run waits for an offline agent: the console shows \"starts when the agent " +
        "connects, waiting until <time>\". null once the run left the queue, and for a run that never waited"
private const val PARTIAL =
    "The backup step has an output of a failed result: a snapshot exists but is incomplete. The status stays failed"

private const val CATCH_UP = "Set only for trigger catch_up: the downtime it stands for"

private const val REQUESTED_BY =
    "Who started a manual run: \"admin\" in the open core, which has one administrator; null for a schedule"

@Schema(description = "What a restore run restores")
data class RunRestore(
    @field:Schema(description = "restic snapshot id")
    val snapshotId: String,
    val snapshotCreatedAt: Instant,
    @field:Schema(description = "The repository on the agent that restores, by the name that agent has for it")
    val repositoryName: String,
    @field:Schema(description = "The restore config the agent got: destination and paths; no secrets")
    val config: Map<String, Any?>,
    @field:Schema(description = "Where it goes as the server computed it; the step's restore has the real one")
    val target: String,
)

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
    val kind: RunKind,
    @field:Schema(description = REQUESTED_BY)
    val requestedBy: String?,
    @field:Schema(description = OUTCOME)
    val outcome: RunOutcome,
    @field:Schema(description = AGENT_WAIT_UNTIL)
    val agentWaitUntil: Instant?,
    @field:Schema(
        description =
            "The timeout the run's step was created with, in seconds, as RunStep.timeoutSeconds: the list says " +
                "\"timed out (6 h)\" with it. null for records before J1b",
    )
    val timeoutSeconds: Long?,
    @field:Schema(description = PARTIAL)
    val partial: Boolean,
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
    @field:Schema(
        description = "The console can restore this snapshot: not partial, not forgotten, a plugin it restores",
    )
    val restorable: Boolean,
    @field:Schema(description = "restic's number of files of the snapshot; null from an agent older than V2")
    val totalFiles: Long?,
    @field:Schema(
        description =
            "The fingerprint of the source the backup took, which a restore check compares with the copy; null " +
                "when the agent or the plugin takes none (the check then stops at level 3)",
    )
    val fingerprint: Fingerprint?,
    @field:Schema(
        description =
            "What the server noticed: the number of files or the size fell by more than 30% since the last " +
                "snapshot of the source. The backup still succeeded",
    )
    val warnings: List<String>,
    @field:Schema(
        description =
            "How many distinct paths restic could not read; null when the agent did not say (older than PL7-2) " +
                "or the output is not partial",
    )
    val unreadableCount: Long?,
    @field:Schema(
        description =
            "The first of them (at most 10) in the order restic reported them: paths only, masked by the agent, " +
                "each cut at 4096 characters; empty when unreadableCount is null or 0",
    )
    val unreadablePaths: List<String>,
    @field:Schema(description = "Why the snapshot is incomplete; null when the output is not partial")
    val incompleteReason: IncompleteReason?,
)

@Schema(
    description =
        "The fingerprint of a source as the console shows it. The facts of the hashed sample of files (their paths " +
            "and the SHA-256 of their content) are only counted: they stay in the snapshot and with the verifier",
)
data class Fingerprint(
    @field:Schema(description = "Lower-case hex SHA-256 of the canonical fingerprint: the snapshot's tag sard.fp")
    val sha256: String,
    @field:Schema(description = "Version of the format of the fingerprint")
    val format: Int,
    val plugin: String,
    val facts: List<FingerprintFact>,
    @field:Schema(description = "How many facts of the hashed sample are not shown")
    val hiddenFacts: Int,
)

@Schema(description = "One measured property of a source, such as the rows of a table")
data class FingerprintFact(
    val name: String,
    @field:Schema(description = "exact: the copy must have the same value; info: shown, never compared")
    val rule: String,
    val value: String,
)

@Schema(
    description =
        "What a restore step reported. A restore is all or nothing: a succeeded one has everything it did, any " +
            "other has this only when its rollback failed, and then leftover alone. A field the restore does not " +
            "have is null: a database restore has no files, a restore of files no tables",
)
data class RestoreOutput(
    @field:Schema(description = "The absolute path of the directory, or the name of the database")
    val target: String?,
    val filesRestored: Long?,
    val bytesRestored: Long?,
    val database: String?,
    val tablesRestored: Long?,
    val rolesRestored: Long?,
    @field:Schema(description = "Whether the owners and groups of the snapshot were restored; false for files")
    val ownersRestored: Boolean?,
    @field:Schema(description = "What the rollback could not remove and the operator must remove by hand")
    val leftover: String?,
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
    @field:Schema(
        description =
            "Set when a restore step reported an output, whatever its status: a lost step shows a late one (R2)",
    )
    val restore: RestoreOutput?,
    @field:Schema(
        description =
            "How a restore step ended: restored, nothing (all or nothing: the step failed and left no trace), " +
                "rollback_failed (restore.leftover must be removed by hand) or late (a lost step's late output is " +
                "at restore.target); null while it runs and for any other action",
    )
    val restoreOutcome: RestoreOutcome?,
    val queuedAt: Instant,
    val dispatchedAt: Instant?,
    val startedAt: Instant?,
    val finishedAt: Instant?,
    @field:Schema(
        description =
            "What the agent reported after the server closed the step as cancelled (its agent did not confirm the " +
                "cancel); null when nothing came. The step's and the run's status do not change",
    )
    val lateStatus: StepStatus?,
    @field:Schema(
        description =
            "The timeout the step was created with, in seconds: the source's, or the installation's default at the " +
                "time. null for a step without one (a verification's is its attempt's; records before J1b)",
    )
    val timeoutSeconds: Long?,
    @field:Schema(
        description =
            "Who closed a timed out step: the agent (its own deadline) or the server (the agent stayed silent " +
                "past the timeout and the lost window); null for any other step",
    )
    val timedOutBy: StepTimedOutBy?,
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
    val kind: RunKind,
    @field:Schema(description = REQUESTED_BY)
    val requestedBy: String?,
    @field:Schema(description = "Set for a restore run; null for a backup")
    val restore: RunRestore?,
    @field:Schema(description = OUTCOME)
    val outcome: RunOutcome,
    @field:Schema(description = "Set once somebody asked to cancel the run; null otherwise")
    val cancellation: Cancellation?,
    @field:Schema(description = AGENT_WAIT_UNTIL)
    val agentWaitUntil: Instant?,
    @field:Schema(
        description =
            "The agent's last contact when the wait expired (outcome agent_unavailable); null: the agent never " +
                "connected, or the run did not expire",
    )
    val agentUnavailableSince: Instant?,
    @field:Schema(description = PARTIAL)
    val partial: Boolean,
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
        kind: RunKind?,
        cursor: String?,
        limit: Int,
    ): RunPage

    fun getRun(runId: UUID): Run

    fun cancelRun(runId: UUID): Run

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
        @Parameter(description = "Only runs of this kind: backup or restore")
        @RequestParam(required = false)
        kind: RunKind?,
        @PageCursor @RequestParam(required = false) cursor: String?,
        @PageLimit @RequestParam(defaultValue = DEFAULT_LIMIT) limit: Int,
    ): RunPage = api.listRuns(sourceId, agentId, status, queuedFrom, queuedTo, kind, cursor, limit)

    @GetMapping("/{runId}")
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "Run card with steps")
    @NotFound
    fun getRun(
        @PathVariable runId: UUID,
    ): Run = api.getRun(runId)

    @PostMapping("/{runId}/cancel")
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Cancel a run",
        description =
            "A queued run is cancelled at once and the agent is told nothing. A run in flight is marked and its " +
                "agent is asked to stop: the answer is the run as it is, outcome cancelling, until the agent " +
                "confirms (cancelled) or does not come back within the lost window (cancel_unconfirmed). Asking " +
                "again changes nothing. A run that is final answers 409 run_finished. An attempt of a restore " +
                "verification cancels its job, as POST /verification-jobs/{jobId}/cancel does.",
    )
    @NotFound
    @CancelRefused
    fun cancelRun(
        @PathVariable runId: UUID,
    ): Run = api.cancelRun(runId)

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
