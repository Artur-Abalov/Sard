// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import dev.sard.server.runs.RunState
import dev.sard.server.runs.Trigger
import java.time.Instant
import java.util.UUID

/** What fired, as stored in `schedule_fires.kind`: a cron moment, or the catch-up after a downtime (D16). */
enum class FireKind(
    val stored: String,
    val trigger: Trigger,
) {
    SCHEDULE("schedule", Trigger.SCHEDULE),
    CATCH_UP("catch_up", Trigger.CATCH_UP),
    ;

    companion object {
        fun of(stored: String): FireKind = entries.single { it.stored == stored }
    }
}

/** What came of a fire, as stored; a [skip] counts toward the alert, a downtime does not (F3a answer 4). */
enum class FireOutcome(
    val stored: String,
    val skip: Boolean,
) {
    RUN_CREATED("run_created", false),
    SKIPPED_ACTIVE("skipped_active", true),
    SKIPPED_GONE("skipped_gone", true),
    REFUSED("refused", true),
    SKIPPED_DOWNTIME("skipped_downtime", false),
    ;

    companion object {
        fun of(stored: String): FireOutcome = entries.single { it.stored == stored }
    }
}

/** Why a fire was gone or refused, as stored: the run refusals of S6a that a schedule can meet. */
enum class FireReason(
    val stored: String,
) {
    SOURCE_DELETED("source_deleted"),
    AGENT_REVOKED("agent_revoked"),
    UNKNOWN_PLUGIN("unknown_plugin"),
    UNKNOWN_REPOSITORY("unknown_repository"),
    ;

    companion object {
        fun of(stored: String): FireReason = entries.single { it.stored == stored }
    }
}

/** A schedule as a caller asks for it: standard 5-field cron in an IANA zone. */
data class ScheduleDraft(
    val cron: String,
    val timezone: String,
    val enabled: Boolean,
    /** Tell of every successful scheduled run too; failures and recoveries are always told (F3b). */
    val notifyOnSuccess: Boolean = false,
)

/** The latest run a schedule created, scheduled or catch-up (F3b). */
data class LastRun(
    val id: UUID,
    val trigger: Trigger,
    val status: RunState,
    val queuedAt: Instant,
    val finishedAt: Instant?,
)

/** A source's schedule; [nextRunAt] is null while disabled, [catchUpAt] set while a catch-up is owed. */
data class ScheduleView(
    val id: UUID,
    val sourceId: UUID,
    val cron: String,
    val timezone: String,
    val enabled: Boolean,
    val nextRunAt: Instant?,
    val catchUpAt: Instant?,
    val lastFiredAt: Instant?,
    val skippedInRow: Int,
    val notifyOnSuccess: Boolean,
    val lastRun: LastRun?,
    val createdAt: Instant,
    val updatedAt: Instant,
)

/** One journal row; a downtime row covers [missedCount] fires from [scheduledFor] through [missedUntil]. */
data class FireView(
    val id: UUID,
    val kind: FireKind,
    val scheduledFor: Instant,
    val outcome: FireOutcome,
    val runId: UUID?,
    val reason: FireReason?,
    val missedCount: Int?,
    val missedUntil: Instant?,
    /** The count stopped at its limit: more fires were missed. */
    val missedCountCapped: Boolean,
    val skippedInRow: Int,
    val alert: Boolean,
    val recordedAt: Instant,
)
