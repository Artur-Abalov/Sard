// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import org.hibernate.Session
import java.time.Instant
import java.util.UUID

/** A finished run that has no delivery through a channel yet. */
data class Unplanned(
    val tenantId: UUID,
    val runId: UUID,
)

/** An overdue episode that opened, or that a success closed, with no delivery through a channel yet (F6). */
data class UnplannedEpisode(
    val tenantId: UUID,
    val episodeId: UUID,
    /** overdue (it opened) or overdue_over (a success closed it). */
    val subject: String,
)

/** A pending delivery whose time has come; of a run ([runId]) or of an overdue episode ([episodeId], F6). */
data class DueDelivery(
    val tenantId: UUID,
    val id: UUID,
    val runId: UUID?,
    val channel: String,
    val attempts: Int,
    val createdAt: Instant,
    val subject: String = SUBJECT_RUN,
    val episodeId: UUID? = null,
) {
    /** What the delivery tells of, for the log: "run <id>", or the episode's subject and id. */
    override fun toString(): String = runId?.let { "run $it" } ?: "$subject episode $episodeId"
}

internal const val SUBJECT_RUN = "run"
internal const val SUBJECT_OVERDUE = "overdue"
internal const val SUBJECT_OVERDUE_OVER = "overdue_over"

private const val UNPLANNED = """
    select new dev.sard.server.notify.Unplanned(r.tenantId, r.id) from RunRecord r
    where r.finishedAt >= :since
      and not exists (
        select 1 from NotificationDeliveryRecord d
        where d.tenantId = r.tenantId and d.runId = r.id and d.channel = :channel)
    order by r.finishedAt, r.id"""

private const val OPENED = """
    select new dev.sard.server.notify.UnplannedEpisode(e.tenantId, e.id, 'overdue') from OverdueEpisodeRecord e
    where e.openedAt >= :since
      and not exists (
        select 1 from NotificationDeliveryRecord d
        where d.tenantId = e.tenantId and d.episodeId = e.id and d.subject = 'overdue' and d.channel = :channel)
    order by e.openedAt, e.id"""

private const val CLOSED = """
    select new dev.sard.server.notify.UnplannedEpisode(e.tenantId, e.id, 'overdue_over') from OverdueEpisodeRecord e
    where e.closedAt >= :since and e.closedByRunId is not null
      and not exists (
        select 1 from NotificationDeliveryRecord d
        where d.tenantId = e.tenantId and d.episodeId = e.id and d.subject = 'overdue_over' and d.channel = :channel)
    order by e.closedAt, e.id"""

private const val DUE = """
    select new dev.sard.server.notify.DueDelivery(
        d.tenantId, d.id, d.runId, d.channel, d.attempts, d.createdAt, d.subject, d.episodeId)
    from NotificationDeliveryRecord d
    where d.status = 'pending' and d.nextAttemptAt <= :now and d.channel in :channels
    order by d.nextAttemptAt, d.id"""

private const val PENDING = "select count(*) from NotificationDeliveryRecord d where d.status = 'pending'"

private const val PLAN = """
    insert into notification_deliveries (id, tenant_id, run_id, subject, channel, status, next_attempt_at, created_at)
    values (:id, :tenant, :run, 'run', :channel, 'pending', :now, :now)
    on conflict (tenant_id, run_id, channel) do nothing"""

private const val PLAN_EPISODE = """
    insert into notification_deliveries (id, tenant_id, episode_id, subject, channel, status, next_attempt_at, created_at)
    values (:id, :tenant, :episode, :subject, :channel, 'pending', :now, :now)
    on conflict (tenant_id, episode_id, subject, channel) do nothing"""

private const val CLAIM = """
    update notification_deliveries set next_attempt_at = :until
    where tenant_id = :tenant and id = :id and status = 'pending' and next_attempt_at <= :now"""

private const val GUARD = "where tenant_id = :tenant and id = :id and status = 'pending'"

private const val RETRY =
    "update notification_deliveries set next_attempt_at = :at, attempts = :attempts, last_error = :error $GUARD"

private const val CLOSE = """
    update notification_deliveries
    set status = :status, finished_at = :now, attempts = :attempts, last_error = coalesce(:error, last_error) $GUARD"""

/**
 * The notification queue (S9a, OQ-047). Finding work reads across tenants in the read-only
 * system session (ADR 0013, list of `system` callers; answer В2); every write runs in the row's
 * own tenant and is a guarded statement, so a lost race changes nothing.
 */
class Deliveries(
    private val sessions: TenantSessions,
    private val ids: UuidV7,
) {
    /** Finished runs since [since] without a delivery through [channel], oldest first. */
    fun unplanned(
        channel: String,
        since: Instant,
        limit: Int,
    ): List<Unplanned> =
        sessions.system { session ->
            session
                .createSelectionQuery(UNPLANNED, Unplanned::class.java)
                .setParameter("since", since)
                .setParameter("channel", channel)
                .setMaxResults(limit)
                .list()
        }

    /** Pending deliveries through [channels] due at [now], longest waiting first. */
    fun due(
        now: Instant,
        channels: Collection<String>,
        limit: Int,
    ): List<DueDelivery> =
        sessions.system { session ->
            session
                .createSelectionQuery(DUE, DueDelivery::class.java)
                .setParameter("now", now)
                .setParameterList("channels", channels)
                .setMaxResults(limit)
                .list()
        }

    fun pending(): Long = sessions.system { it.createSelectionQuery(PENDING, Long::class.java).singleResult }

    /** One pending delivery through [channel] for each of [runs] of [tenantId]; returns how many are new. */
    fun plan(
        tenantId: UUID,
        runs: List<UUID>,
        channel: String,
        now: Instant,
    ): Int =
        sessions.inTenant(tenantId) { session ->
            runs.sumOf { run ->
                session
                    .createNativeMutationQuery(PLAN)
                    .setParameter("id", ids.next())
                    .setParameter("tenant", tenantId)
                    .setParameter("run", run)
                    .setParameter("channel", channel)
                    .setParameter("now", now)
                    .executeUpdate()
            }
        }

    /**
     * Takes [delivery] for one attempt: it is not due again until [until], so a crash during the
     * send leads to another attempt then. Returns what to tell about, or null when another
     * sender took it first.
     */
    fun claim(
        delivery: DueDelivery,
        now: Instant,
        until: Instant,
    ): Notice? =
        sessions.inTenant(delivery.tenantId) { session ->
            val claimed =
                session
                    .createNativeMutationQuery(CLAIM)
                    .setParameter("until", until)
                    .setParameter("tenant", delivery.tenantId)
                    .setParameter("id", delivery.id)
                    .setParameter("now", now)
                    .executeUpdate() == 1
            if (claimed) Notices.of(session, delivery) else null
        }

    /** Stores how the attempt ended; false when the delivery is no longer pending. */
    fun record(
        delivery: DueDelivery,
        decision: Decision,
        now: Instant,
    ): Boolean =
        sessions.inTenant(delivery.tenantId) { session ->
            val query =
                when (decision) {
                    is Decision.Retry -> {
                        session
                            .createNativeMutationQuery(RETRY)
                            .setParameter("at", decision.at)
                            .setParameter("attempts", decision.attempts)
                            .setParameter("error", decision.reason, String::class.java)
                    }

                    else -> {
                        close(session, decision, delivery.attempts, now)
                    }
                }
            query.setParameter("tenant", delivery.tenantId).setParameter("id", delivery.id).executeUpdate() == 1
        }

    private fun close(
        session: Session,
        decision: Decision,
        attempts: Int,
        now: Instant,
    ) = session
        .createNativeMutationQuery(CLOSE)
        .setParameter("status", closedStatus(decision))
        .setParameter("now", now)
        .setParameter("attempts", closedAttempts(decision, attempts))
        .setParameter("error", closedReason(decision), String::class.java)
}

/**
 * The overdue episodes' side of the queue (F6): finding episodes without a delivery reads across tenants in the
 * read-only system session (ADR 0013, caller 5); planning writes in the episode's own tenant, guarded by the key.
 */
class OverdueDeliveries(
    private val sessions: TenantSessions,
    private val ids: UuidV7,
) {
    /** Overdue episodes opened or closed by a success since [since] without a delivery through [channel] (F6). */
    fun unplanned(
        channel: String,
        since: Instant,
        limit: Int,
    ): List<UnplannedEpisode> =
        sessions.system { session ->
            listOf(OPENED, CLOSED).flatMap { hql ->
                session
                    .createSelectionQuery(hql, UnplannedEpisode::class.java)
                    .setParameter("since", since)
                    .setParameter("channel", channel)
                    .setMaxResults(limit)
                    .list()
            }
        }

    /** One pending delivery through [channel] for each of [episodes] of [tenantId]; returns how many are new. */
    fun plan(
        tenantId: UUID,
        episodes: List<UnplannedEpisode>,
        channel: String,
        now: Instant,
    ): Int =
        sessions.inTenant(tenantId) { session ->
            episodes.sumOf { episode ->
                session
                    .createNativeMutationQuery(PLAN_EPISODE)
                    .setParameter("id", ids.next())
                    .setParameter("tenant", tenantId)
                    .setParameter("episode", episode.episodeId)
                    .setParameter("subject", episode.subject)
                    .setParameter("channel", channel)
                    .setParameter("now", now)
                    .executeUpdate()
            }
        }
}

private fun closedStatus(decision: Decision): String =
    when (decision) {
        Decision.Delivered -> "delivered"
        Decision.Skipped -> "skipped"
        is Decision.Failed -> "failed"
        is Decision.Expired -> "expired"
        is Decision.Retry -> error("a retry keeps the delivery pending")
    }

private fun closedAttempts(
    decision: Decision,
    attempts: Int,
): Int =
    when (decision) {
        is Decision.Failed -> decision.attempts
        is Decision.Expired -> decision.attempts
        Decision.Delivered, Decision.Skipped, is Decision.Retry -> attempts
    }

private fun closedReason(decision: Decision): String? =
    when (decision) {
        is Decision.Failed -> decision.reason
        is Decision.Expired -> decision.reason
        Decision.Delivered, Decision.Skipped, is Decision.Retry -> null
    }
