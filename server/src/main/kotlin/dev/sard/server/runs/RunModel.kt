// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import java.time.Instant
import java.util.UUID

/** A run's status as stored (ADR 0013, rule 7); queued, dispatched and running are active (D6). */
enum class RunState(
    val stored: String,
    val active: Boolean,
) {
    QUEUED("queued", true),
    DISPATCHED("dispatched", true),
    RUNNING("running", true),
    SUCCEEDED("succeeded", false),
    FAILED("failed", false),
    CANCELLED("cancelled", false),
    ;

    companion object {
        fun of(stored: String): RunState = entries.single { it.stored == stored }

        /** A stage 1 run has one step: it follows it; any failure of the step fails the run (S8a, answer 2). */
        fun following(step: StepState): RunState =
            when (step) {
                StepState.QUEUED -> QUEUED
                StepState.DISPATCHED -> DISPATCHED
                StepState.RUNNING -> RUNNING
                StepState.SUCCEEDED -> SUCCEEDED
                StepState.CANCELLED -> CANCELLED
                StepState.FAILED, StepState.TIMED_OUT, StepState.REJECTED, StepState.LOST -> FAILED
            }
    }
}

/** A step's status as stored; the proto's final states plus the server's own (ADR 0013, S8a). */
enum class StepState(
    val stored: String,
    val active: Boolean = false,
) {
    QUEUED("queued", true),
    DISPATCHED("dispatched", true),
    RUNNING("running", true),
    SUCCEEDED("succeeded"),
    FAILED("failed"),
    CANCELLED("cancelled"),
    TIMED_OUT("timed_out"),
    REJECTED("rejected"),
    LOST("lost"),
    ;

    companion object {
        fun of(stored: String): StepState = entries.single { it.stored == stored }
    }
}

/** What started a run; stage 1 starts runs manually only. */
enum class Trigger(
    val stored: String,
) {
    SCHEDULE("schedule"),
    MANUAL("manual"),
    VERIFICATION("verification"),
    ;

    companion object {
        fun of(stored: String): Trigger = entries.single { it.stored == stored }
    }
}

/** What a step asks the agent to do (proto Action). */
enum class Action(
    val stored: String,
) {
    BACKUP("backup"),
    RESTORE("restore"),
    VERIFY("verify"),
    RUN("run"),
    ;

    companion object {
        fun of(stored: String): Action = entries.single { it.stored == stored }
    }
}

/** A source as a caller asks for it; [config] is JSON object text with secret names only (ADR 0008). */
data class SourceDraft(
    val name: String,
    val agentId: UUID,
    val plugin: String,
    val repositoryName: String,
    val config: String,
) {
    override fun toString(): String {
        val names = "plugin=$plugin, repositoryName=$repositoryName"
        return "SourceDraft(name=$name, agentId=$agentId, $names)"
    }
}

/** A live source. */
data class SourceView(
    val id: UUID,
    val name: String,
    val agentId: UUID,
    val plugin: String,
    val repositoryName: String,
    val config: String,
    val createdAt: Instant,
    val updatedAt: Instant,
) {
    override fun toString() = "SourceView(id=$id, name=$name, agentId=$agentId, plugin=$plugin)"
}

/** What a backup step produced, from its stored output. */
data class BackupResult(
    val snapshotId: String,
    val totalBytes: Long,
    val addedBytes: Long,
    val repositoryId: String,
    val partial: Boolean = false,
)

/** A run with its steps; stage 1 runs have exactly one. */
data class RunView(
    val id: UUID,
    val sourceId: UUID,
    /** The source's current name; a deleted source's name at the time it was deleted. */
    val sourceName: String,
    val sourceDeleted: Boolean,
    val agentId: UUID,
    val trigger: Trigger,
    val status: RunState,
    val message: String?,
    val queuedAt: Instant,
    val startedAt: Instant?,
    val finishedAt: Instant?,
    val steps: List<StepView> = emptyList(),
)

/** One step; [id] is the command_id the agent sees. Progress fields stay null until S7 fills them. */
data class StepView(
    val id: UUID,
    val ordinal: Int,
    val action: Action,
    val status: StepState,
    val agentId: UUID,
    val sourceId: UUID?,
    val plugin: String,
    val repositoryName: String?,
    val config: String,
    val queuedAt: Instant,
    val dispatchedAt: Instant?,
    val phase: String? = null,
    val bytesProcessed: Long? = null,
    val bytesTotal: Long? = null,
    val message: String? = null,
    val startedAt: Instant? = null,
    val finishedAt: Instant? = null,
    val filesProcessed: Long? = null,
    val filesTotal: Long? = null,
    /** The valid backup output the agent sent, whatever the step's status (S8b В8); null if none. */
    val backup: BackupResult? = null,
    /** The run the step belongs to; the agent tags its snapshot with it (FXs Д5). */
    val runId: UUID,
) {
    override fun toString() = "StepView(id=$id, action=$action, status=$status, agentId=$agentId)"
}
