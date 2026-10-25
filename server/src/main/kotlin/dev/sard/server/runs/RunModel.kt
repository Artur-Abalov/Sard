// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.role.AgentRole
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

/** What started a run: a person, a schedule on time, or the one catch-up after a downtime (D16). */
enum class Trigger(
    val stored: String,
) {
    SCHEDULE("schedule"),
    MANUAL("manual"),
    VERIFICATION("verification"),
    CATCH_UP("catch_up"),
    ;

    companion object {
        fun of(stored: String): Trigger = entries.single { it.stored == stored }
    }
}

/** What a run is: a backup of a source or a restore of one of its snapshots (R2). */
enum class RunKind(
    val stored: String,
) {
    BACKUP("backup"),
    RESTORE("restore"),
    ;

    companion object {
        fun of(stored: String): RunKind = entries.single { it.stored == stored }
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

/**
 * What each role may be asked to do (V1): a source runs BACKUP, RESTORE and RUN; a verifier only VERIFY. The
 * one place the rule lives; sources, runs and the dispatcher ask it. Exhaustive, no `else`.
 */
fun AgentRole.allows(action: Action): Boolean =
    when (this) {
        AgentRole.SOURCE -> {
            when (action) {
                Action.BACKUP, Action.RESTORE, Action.RUN -> true
                Action.VERIFY -> false
            }
        }

        AgentRole.VERIFIER -> {
            when (action) {
                Action.VERIFY -> true
                Action.BACKUP, Action.RESTORE, Action.RUN -> false
            }
        }
    }

/** A source the server creates and keeps itself (F6): the self-backup's two; null for a user's source. */
enum class SystemRole(
    val stored: String,
) {
    SELF_DATABASE("self_database"),
    SELF_KEYS("self_keys"),
    ;

    companion object {
        fun of(stored: String): SystemRole = entries.single { it.stored == stored }
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
    val systemRole: SystemRole? = null,
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
    /** The console can restore this snapshot (R2): not partial, not forgotten, a plugin it restores. */
    val restorable: Boolean = false,
    /** restic's number of files; null before V2. */
    val totalFiles: Long? = null,
    /** What the console shows of the fingerprint of the source (V2); null without one. */
    val fingerprint: FingerprintSummary? = null,
    /** What the server noticed about the backup, such as a fall of its volume (V2). */
    val warnings: List<String> = emptyList(),
)

/** What a restore run was asked for, as the console shows it; [config] is the JSON text the agent got. */
data class RestoreInfo(
    val snapshotId: String,
    val snapshotCreatedAt: Instant,
    /** The repository on the agent that restores, which need not be the one the snapshot was made through. */
    val repositoryName: String,
    val config: String,
    /** Where the restore goes, as the server computed it when it was asked; the step's output has the real one. */
    val target: String,
) {
    override fun toString() = "RestoreInfo(snapshotId=$snapshotId, repositoryName=$repositoryName)"
}

/**
 * What a restore step reports. A succeeded one has its result; a failed one only [leftover], when its rollback
 * failed; null stands for a field that did not come (R2, all or nothing).
 */
data class RestoreResult(
    val target: String?,
    val filesRestored: Long?,
    val bytesRestored: Long?,
    val database: String?,
    val tablesRestored: Long?,
    val rolesRestored: Long?,
    val ownersRestored: Boolean?,
    val leftover: String?,
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
    val kind: RunKind = RunKind.BACKUP,
    /** Who started a manual run; null for a schedule (the open core has one administrator). */
    val requestedBy: String? = null,
    /** What a restore run restores; null for a backup. */
    val restore: RestoreInfo? = null,
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
    /** The restore output the agent sent, whatever the step's status (R2); null if none. */
    val restore: RestoreResult? = null,
    /** The snapshot a restore step restores or a VERIFY step checks: restic's full id; null for any other step. */
    val snapshotId: String? = null,
    /** The run the step belongs to; the agent tags its snapshot with it (FXs Д5). */
    val runId: UUID,
    /** What a VERIFY step carries besides its snapshot (V4); null for any other step. */
    val verifyTask: VerifyTask? = null,
) {
    override fun toString() = "StepView(id=$id, action=$action, status=$status, agentId=$agentId)"
}
