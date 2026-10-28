// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.fleet.CertificateEvent
import dev.sard.server.runs.IncompleteReason
import dev.sard.server.runs.RestoreResult
import dev.sard.server.runs.RunKind
import dev.sard.server.runs.RunState
import dev.sard.server.runs.StepState
import dev.sard.server.runs.Trigger
import dev.sard.server.scheduler.FireOutcome
import dev.sard.server.scheduler.FireReason
import java.time.Duration
import java.time.Instant
import java.util.UUID

/**
 * A finished run as a notification sees it (S9a): [RunFinished][dev.sard.server.runs.RunFinished]
 * read back from the database at send time, with the names a person recognises.
 */
data class RunNotice(
    val tenantId: UUID,
    val runId: UUID,
    val trigger: Trigger,
    val status: RunState,
    /** Why it failed; null on success. */
    val message: String?,
    val queuedAt: Instant,
    val finishedAt: Instant,
    val sourceId: UUID,
    val sourceName: String,
    val agentId: UUID,
    val agentHostname: String,
    /** The status of the run's only step (stage 1): the run's [status] cannot tell failed from lost. */
    val stepStatus: StepState,
    /** The run's `started_at`; null when the step never started (rejected before ACCEPTED). */
    val startedAt: Instant?,
    /** The step's backup output, if it has one: kept even when the step failed after saving a snapshot. */
    val backup: BackupSizes?,
    /** The status of the source's run finished before this one; null for its first run (F3a). */
    val previousStatus: RunState? = null,
    /** How many runs of the source failed in a row right before this one, of any trigger (F3b). */
    val failuresBefore: Int = 0,
    /** The schedule asks to hear of successful scheduled runs too; read when the message is sent (F3b). */
    val notifyOnSuccess: Boolean = false,
    /** A restore is told on its own terms, always (R2, ВК39). */
    val kind: RunKind = RunKind.BACKUP,
    /** What a restore restored; null for a backup. */
    val restore: RestoreNotice? = null,
)

/** The snapshot a restore run restores and what its step reported of it; [result] is null when it reported none. */
data class RestoreNotice(
    /** restic's full id. */
    val snapshotId: String,
    val snapshotCreatedAt: Instant,
    val result: RestoreResult?,
)

/**
 * A schedule fire that raised the alert (F3b, ADR 0055), read back at send time with the names a person
 * recognises: the source and the agent's host, never configuration or repository.
 */
data class SkipAlertNotice(
    val tenantId: UUID,
    val fireId: UUID,
    val sourceId: UUID,
    val sourceName: String,
    val agentHostname: String,
    /** Fires skipped in a row, as the journal row counted them. */
    val skippedInRow: Int,
    val outcome: FireOutcome,
    val reason: FireReason?,
    /** The cron moment that was skipped. */
    val scheduledFor: Instant,
    /** The schedule's zone, to write [scheduledFor] in. */
    val timezone: String,
    /** The run that was active, for a fire skipped because of it. */
    val activeRunId: UUID?,
)

/** What a notification tells of a run (ADR 0024): a manual run's result, a scheduled run's turn of the series. */
enum class Telling {
    RESULT,
    FIRST_FAILURE,
    RECOVERY,
    ;

    companion object {
        /** Null when the run needs no notification: a scheduled run in the middle of a series, a verification. */
        fun of(notice: RunNotice): Telling? =
            if (notice.kind == RunKind.RESTORE) {
                RESULT // a restore is a person's act and is always told: no series, no skipping (ВК39)
            } else {
                ofBackup(notice)
            }

        private fun ofBackup(notice: RunNotice): Telling? =
            when (notice.trigger) {
                Trigger.MANUAL -> RESULT
                Trigger.SCHEDULE, Trigger.CATCH_UP -> turn(notice)
                Trigger.VERIFICATION -> null
            }

        private fun turn(notice: RunNotice): Telling? {
            val failedBefore = notice.previousStatus == RunState.FAILED
            return when {
                notice.status == RunState.FAILED -> FIRST_FAILURE.takeUnless { failedBefore }
                notice.status == RunState.SUCCEEDED -> success(failedBefore, notice.notifyOnSuccess)
                else -> null
            }
        }

        /** A success ends a series of failures, or is told on its own when the schedule asks for it. */
        private fun success(
            failedBefore: Boolean,
            notifyOnSuccess: Boolean,
        ): Telling? =
            when {
                failedBefore -> RECOVERY
                notifyOnSuccess -> RESULT
                else -> null
            }
    }
}

/** How much a backup saw and how much of it was new, in bytes, and what is known of its incompleteness. */
data class BackupSizes(
    val totalBytes: Long,
    val addedBytes: Long,
    /** Why the snapshot is incomplete; null for a whole one (PL7-2). */
    val incompleteReason: IncompleteReason? = null,
    /** How many paths restic could not read; null when the agent did not say. */
    val unreadableCount: Long? = null,
    /** The first of them, as the server keeps them: paths only, masked by the agent (A7b). */
    val unreadablePaths: List<String> = emptyList(),
)

/**
 * The text of one notification, independent of any channel. Every part holds plain text: a
 * channel escapes it for its own markup, so a formatter cannot inject markup by mistake.
 */
data class Message(
    val parts: List<Part>,
) {
    constructor(vararg parts: Part) : this(parts.toList())

    sealed interface Part {
        val text: String
    }

    data class Text(
        override val text: String,
    ) : Part

    data class Bold(
        override val text: String,
    ) : Part

    /** Monospaced: identifiers, host names, error text. */
    data class Code(
        override val text: String,
    ) : Part

    companion object {
        private val NEWLINE = listOf<Part>(Text("\n"))

        /** The message of [lines], one under another. */
        fun ofLines(lines: List<List<Part>>): Message =
            Message(lines.flatMapIndexed { index, line -> if (index == 0) line else NEWLINE + line })
    }
}

/** Rules and wording (S9b): what to say about a run, or null when the run needs no notification. */
fun interface NotificationFormatter {
    fun format(notice: RunNotice): Message?
}

/**
 * An overdue episode of a scheduled source (F6, ADR 00XX-draft-source-freshness) as a notification sees it:
 * [closedBy] is null when it opened, and the success that closed it otherwise.
 */
data class OverdueNotice(
    val tenantId: UUID,
    val episodeId: UUID,
    val sourceId: UUID,
    val sourceName: String,
    val agentHostname: String,
    /** The last success it counts from; null when the source never succeeded. */
    val countedFrom: Instant?,
    val deadline: Instant,
    val closedBy: RunNotice?,
)

/** The wording of an overdue episode (F6); null when its end is told by the run that ended it. */
fun interface OverdueFormatter {
    fun format(notice: OverdueNotice): Message?
}

/**
 * An agent certificate that reached a threshold of its lifetime or expired (C1 phase 3, П36), as a notification sees
 * it: read back at send time, with what the server knows of the agent. Never the certificate itself.
 */
data class CertificateNotice(
    val tenantId: UUID,
    val agentId: UUID,
    val hostname: String,
    /** The agent next to the server (sard-self): its advice differs. */
    val builtin: Boolean,
    val event: CertificateEvent,
    val notAfter: Instant,
    /** The moment of sending: the time left is counted from it. */
    val now: Instant,
    /** When a renewal was issued that the agent has not confirmed; null if none. */
    val renewalIssuedAt: Instant?,
    val online: Boolean,
    val lastSeenAt: Instant?,
)

/** Whether an agent holds a stream with the server right now (the stream manager's answer, not a heartbeat). */
fun interface AgentConnected {
    fun online(agentId: UUID): Boolean
}

/** The wording of a notice about an agent certificate (C1 phase 3, П38 and П38б). */
fun interface CertificateFormatter {
    fun format(notice: CertificateNotice): Message
}

/** The wording of the alert about fires skipped in a row (F3b); the one text a skip can have. */
fun interface SkipAlertFormatter {
    fun format(notice: SkipAlertNotice): Message
}

/** One way to reach people (Telegram now; email or a webhook later). */
interface NotificationChannel {
    /** Stored in `notification_deliveries.channel`: lower-case letters, digits and dashes. */
    val name: String

    /** Sends one message; never throws, the outcome says what to do next. */
    fun send(message: Message): SendOutcome
}

/** How one send attempt ended, as [RetryPolicy] needs to know it. */
sealed interface SendOutcome {
    data object Delivered : SendOutcome

    /** The channel asks to wait (Telegram 429 with retry_after). */
    data class RetryAfter(
        val wait: Duration,
    ) : SendOutcome

    /** Worth another attempt: 5xx, network, timeout. */
    data class Transient(
        val reason: String,
    ) : SendOutcome

    /** Another attempt would fail the same way: 4xx other than 429. */
    data class Rejected(
        val reason: String,
    ) : SendOutcome
}
