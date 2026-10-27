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

    /** Not run: the agent was offline until the wait deadline (J1c); not a failure. */
    AGENT_UNAVAILABLE("agent_unavailable", false),
    ;

    companion object {
        fun of(stored: String): RunState = entries.single { it.stored == stored }

        /** A stage 1 run has one step: it follows it; any failure of the step fails the run (S8a, answer 2). */
        fun following(step: StepState): RunState = step.run
    }
}

/** A step's status as stored; the proto's final states plus the server's own (ADR 0013, S8a). */
enum class StepState(
    val stored: String,
    /** The status of a stage 1 run whose only step this is: any failure of the step fails the run (S8a). */
    val run: RunState,
    val active: Boolean = false,
) {
    QUEUED("queued", RunState.QUEUED, true),
    DISPATCHED("dispatched", RunState.DISPATCHED, true),
    RUNNING("running", RunState.RUNNING, true),
    SUCCEEDED("succeeded", RunState.SUCCEEDED),
    FAILED("failed", RunState.FAILED),
    CANCELLED("cancelled", RunState.CANCELLED),
    TIMED_OUT("timed_out", RunState.FAILED),
    REJECTED("rejected", RunState.FAILED),
    LOST("lost", RunState.FAILED),

    /** Never sent: the agent was offline until the run's wait deadline (J1c). */
    AGENT_UNAVAILABLE("agent_unavailable", RunState.AGENT_UNAVAILABLE),
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
    /** The backup and restore timeouts of the source in minutes; null: the tenant's default (J1b). */
    val backupTimeoutMinutes: Int? = null,
    val restoreTimeoutMinutes: Int? = null,
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
    val backupTimeoutMinutes: Int? = null,
    val restoreTimeoutMinutes: Int? = null,
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

/** Who closed a step as timed out. */
enum class TimedOutBy(
    val stored: String,
) {
    /** The agent's own deadline: its StepResult was TIMED_OUT. */
    AGENT("agent"),

    /** The agent stayed silent past the timeout and the lost window (Р-J7). */
    SERVER("server"),
    ;

    companion object {
        fun of(stored: String): TimedOutBy = entries.single { it.stored == stored }
    }
}

/** How a cancel of a run ended; pending until the run is final (J1a). */
enum class CancelResult(
    val stored: String,
) {
    PENDING("pending"),

    /** The agent confirmed, or the run was cancelled before it was sent. */
    CANCELLED("cancelled"),

    /** The agent did not confirm within the lost window. */
    UNCONFIRMED("unconfirmed"),

    /** The agent's result came first and is the run's result. */
    FINISHED_FIRST("finished_first"),
    ;

    companion object {
        fun of(stored: String): CancelResult = entries.single { it.stored == stored }
    }
}

/** A cancel somebody asked for: when, who (the open core has one administrator) and how it ended. */
data class Cancellation(
    val requestedAt: Instant,
    val requestedBy: String,
    val result: CancelResult,
)

/** What a run came to, for a person to read: the server derives it from the run, its step and its cancel. */
enum class RunOutcome {
    QUEUED,

    /** Queued, and the agent is offline: the run starts when it connects, or expires at its wait deadline (J1c). */
    WAITING_FOR_AGENT,
    DISPATCHED,
    RUNNING,
    CANCELLING,
    SUCCEEDED,
    FAILED,
    TIMED_OUT,
    REJECTED,
    LOST,
    CANCELLED,
    CANCEL_UNCONFIRMED,
    AGENT_UNAVAILABLE,
    ;

    companion object {
        /**
         * A failed run is told by its step (ADR 0038: the run has only failed); a cancel in flight is cancelling; a
         * queued run is waiting for its agent when [agentOnline] is false (J1c).
         */
        fun of(
            status: RunState,
            step: StepState?,
            cancellation: Cancellation?,
            agentOnline: Boolean = true,
        ): RunOutcome =
            when (status) {
                RunState.QUEUED -> queued(agentOnline)
                RunState.DISPATCHED, RunState.RUNNING -> inFlight(status, cancellation)
                RunState.SUCCEEDED -> SUCCEEDED
                RunState.FAILED -> failed(step)
                RunState.CANCELLED -> cancelled(cancellation)
                RunState.AGENT_UNAVAILABLE -> AGENT_UNAVAILABLE
            }

        private fun queued(agentOnline: Boolean) = if (agentOnline) QUEUED else WAITING_FOR_AGENT

        private fun inFlight(
            status: RunState,
            cancellation: Cancellation?,
        ) = if (cancellation != null) {
            CANCELLING
        } else if (status == RunState.DISPATCHED) {
            DISPATCHED
        } else {
            RUNNING
        }

        private fun cancelled(cancellation: Cancellation?) =
            if (cancellation?.result == CancelResult.UNCONFIRMED) CANCEL_UNCONFIRMED else CANCELLED

        private fun failed(step: StepState?): RunOutcome =
            when (step) {
                StepState.TIMED_OUT -> TIMED_OUT
                StepState.REJECTED -> REJECTED
                StepState.LOST -> LOST
                else -> FAILED
            }
    }
}

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
    /** The cancel somebody asked for; null when none was. */
    val cancellation: Cancellation? = null,
    /** The status of the run's step when [steps] is left out (a list), so that [outcome] can still tell failures. */
    val stepStatus: StepState? = null,
    /** Until when a queued run waits for an offline agent (J1c); null once there is nothing to wait for. */
    val agentWaitUntil: Instant? = null,
    /** The agent's last contact when the wait expired; null: it never connected, or the run did not expire (J1c). */
    val agentUnavailableSince: Instant? = null,
    /** The run's agent has a session; read only for a queued run, for the others it is true (J1c). */
    val agentOnline: Boolean = true,
) {
    val outcome: RunOutcome
        get() = RunOutcome.of(status, steps.firstOrNull()?.status ?: stepStatus, cancellation, agentOnline)
}

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
    /** The run's cancel was asked for and is not over: the agent gets a CancelStep, never the step again (J1a). */
    val cancelRequested: Boolean = false,
    /** What the agent reported after the server closed the step as cancelled; null if nothing came (J1a). */
    val lateStatus: StepState? = null,
    /** The timeout the step was created with, in seconds; null for a step without one (J1b). */
    val timeoutSeconds: Long? = null,
    /** Who closed a timed out step: the agent's own deadline, or the server (J1b). */
    val timedOutBy: TimedOutBy? = null,
) {
    override fun toString() = "StepView(id=$id, action=$action, status=$status, agentId=$agentId)"
}
