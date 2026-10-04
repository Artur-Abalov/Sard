// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.runs.RunState
import dev.sard.server.runs.StepState
import dev.sard.server.runs.Trigger
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
    /** When the agent began the step; null for a step that never started (rejected before ACCEPTED). */
    val startedAt: Instant?,
    /** The step's backup output, if it has one: kept even when the step failed after saving a snapshot. */
    val backup: BackupSizes?,
)

/** How much a backup saw and how much of it was new, in bytes. */
data class BackupSizes(
    val totalBytes: Long,
    val addedBytes: Long,
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
}

/** Rules and wording (S9b): what to say about a run, or null when the run needs no notification. */
fun interface NotificationFormatter {
    fun format(notice: RunNotice): Message?
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
