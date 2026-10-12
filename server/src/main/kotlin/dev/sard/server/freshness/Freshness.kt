// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.freshness

import dev.sard.server.scheduler.Fires
import java.time.Duration
import java.time.Instant
import java.util.UUID

/** The newest succeeded run of a source. */
data class LastSuccess(
    val runId: UUID,
    val at: Instant,
)

/** A source's open overdue episode and the success it counted from (null: the source never succeeded). */
data class OpenEpisode(
    val id: UUID,
    val countedFrom: Instant?,
)

/** A scheduled source as the freshness check sees it; [scheduleChangedAt] is the schedule's last change. */
data class SourceFreshness(
    val tenantId: UUID,
    val sourceId: UUID,
    val fires: Fires,
    val scheduleChangedAt: Instant,
    val lastSuccess: LastSuccess?,
    val open: OpenEpisode?,
)

/** What the check does with one source. */
sealed interface Verdict {
    data object Keep : Verdict

    /** The source is overdue since [deadline]; it counts from [countedFrom], its last success (null: none). */
    data class Open(
        val deadline: Instant,
        val countedFrom: Instant?,
    ) : Verdict

    /** A success newer than the episode's own: [runId] ends [episodeId]. */
    data class Close(
        val episodeId: UUID,
        val runId: UUID,
    ) : Verdict
}

/**
 * Freshness of a scheduled source (F6, ADR 00XX-draft-source-freshness): it is overdue once one and a half
 * of its schedule's interval has passed since its last success, or since the schedule last changed when that
 * is later (a schedule just set or re-enabled is not overdue at once). The interval is the gap between the
 * schedule's first two fires after that moment, so an irregular cron is judged by the gap it is in.
 */
object Freshness {
    private const val NUMERATOR = 3L
    private const val DENOMINATOR = 2L

    fun deadline(
        from: Instant,
        fires: Fires,
    ): Instant {
        val first = fires.nextAfter(from)
        val interval = Duration.between(first, fires.nextAfter(first))
        return from + interval.multipliedBy(NUMERATOR).dividedBy(DENOMINATOR)
    }

    fun judge(
        source: SourceFreshness,
        now: Instant,
    ): Verdict {
        val open = source.open ?: return opening(source, now)
        val success = source.lastSuccess
        val newer = success != null && (open.countedFrom == null || success.at > open.countedFrom)
        return if (newer) Verdict.Close(open.id, checkNotNull(success).runId) else Verdict.Keep
    }

    private fun opening(
        source: SourceFreshness,
        now: Instant,
    ): Verdict {
        val success = source.lastSuccess?.at
        val from = if (success == null || success < source.scheduleChangedAt) source.scheduleChangedAt else success
        val deadline = deadline(from, source.fires)
        return if (now < deadline) Verdict.Keep else Verdict.Open(deadline, success)
    }
}
