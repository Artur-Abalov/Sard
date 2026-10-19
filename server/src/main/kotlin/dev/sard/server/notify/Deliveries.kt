// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.fleet.CertificateEvent
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import org.hibernate.Session
import org.hibernate.query.MutationQuery
import java.time.Instant
import java.util.UUID

/** A finished run that has no delivery through a channel yet. */
data class Unplanned(
    val tenantId: UUID,
    val runId: UUID,
)

/** A schedule fire that raised an alert and has no delivery through a channel yet (F3b). */
data class UnplannedAlert(
    val tenantId: UUID,
    val fireId: UUID,
)

/** An overdue episode that opened, or that a success closed, with no delivery through a channel yet (F6). */
data class UnplannedEpisode(
    val tenantId: UUID,
    val episodeId: UUID,
    /** opened or closed. */
    val event: String,
)

/** A recorded certificate event that has no delivery through a channel yet (C1 phase 3). */
data class UnplannedCertificateEvent(
    val tenantId: UUID,
    val eventId: UUID,
)

/**
 * A pending delivery whose time has come: about a run, a schedule fire (F3b), an overdue episode's [episodeEvent]
 * (F6) or an agent certificate event (C1 phase 3), exactly one of them.
 */
data class DueDelivery(
    val tenantId: UUID,
    val id: UUID,
    val runId: UUID?,
    val fireId: UUID?,
    val channel: String,
    val attempts: Int,
    val createdAt: Instant,
    val episodeId: UUID? = null,
    val episodeEvent: String? = null,
    val certificateEventId: UUID? = null,
    /** The serial and the stored name of the certificate event, for the log. */
    val certificateSerial: String? = null,
    val certificateEvent: String? = null,
) {
    /** What the delivery is about, as the log names it. */
    val subject: String
        get() =
            when {
                fireId != null -> "schedule fire $fireId"
                certificateEventId != null -> "agent certificate $certificateSerial (${certificateLabel()})"
                episodeId != null -> "overdue episode $episodeId ($episodeEvent)"
                else -> "run $runId"
            }
}

/** "30/365" of a threshold event, "expired" of the expiry (П39). */
private fun DueDelivery.certificateLabel(): String {
    val event = CertificateEvent.of(checkNotNull(certificateEvent))
    return event.days?.let { "$it/365" } ?: "expired"
}

/** The event of an overdue episode a delivery tells of: it opened, or a success closed it. */
internal const val EPISODE_OPENED = "opened"
internal const val EPISODE_CLOSED = "closed"

/** What a claimed delivery tells about, read back at send time. */
sealed interface Claimed {
    data class Finished(
        val notice: RunNotice,
    ) : Claimed

    data class Alert(
        val notice: SkipAlertNotice,
    ) : Claimed

    data class Overdue(
        val notice: OverdueNotice,
    ) : Claimed

    /** [notice] is null when the certificate is no longer the agent's current one: nothing to tell. */
    data class Certificate(
        val notice: CertificateNotice?,
    ) : Claimed
}

private const val UNPLANNED = """
    select new dev.sard.server.notify.Unplanned(r.tenantId, r.id) from RunRecord r
    where r.finishedAt >= :since
      and not exists (
        select 1 from NotificationDeliveryRecord d
        where d.tenantId = r.tenantId and d.runId = r.id and d.channel = :channel)
    order by r.finishedAt, r.id"""

private const val UNPLANNED_ALERTS = """
    select new dev.sard.server.notify.UnplannedAlert(f.tenantId, f.id) from ScheduleFireRecord f
    where f.alert = true and f.recordedAt >= :since
      and not exists (
        select 1 from NotificationDeliveryRecord d
        where d.tenantId = f.tenantId and d.fireId = f.id and d.channel = :channel)
    order by f.recordedAt, f.id"""

private const val OPENED_EPISODES = """
    select new dev.sard.server.notify.UnplannedEpisode(e.tenantId, e.id, 'opened') from OverdueEpisodeRecord e
    where e.openedAt >= :since
      and not exists (
        select 1 from NotificationDeliveryRecord d
        where d.tenantId = e.tenantId and d.episodeId = e.id and d.episodeEvent = 'opened' and d.channel = :channel)
    order by e.openedAt, e.id"""

private const val CLOSED_EPISODES = """
    select new dev.sard.server.notify.UnplannedEpisode(e.tenantId, e.id, 'closed') from OverdueEpisodeRecord e
    where e.closedAt >= :since and e.closedByRunId is not null
      and not exists (
        select 1 from NotificationDeliveryRecord d
        where d.tenantId = e.tenantId and d.episodeId = e.id and d.episodeEvent = 'closed' and d.channel = :channel)
    order by e.closedAt, e.id"""

private const val PLAN_EPISODE = """
    insert into notification_deliveries
        (id, tenant_id, episode_id, episode_event, channel, status, next_attempt_at, created_at)
    values (:id, :tenant, :episode, :event, :channel, 'pending', :now, :now)
    on conflict (tenant_id, episode_id, episode_event, channel) do nothing"""

private const val UNPLANNED_CERTIFICATES = """
    select new dev.sard.server.notify.UnplannedCertificateEvent(e.tenantId, e.id) from AgentCertificateEventRecord e
    where e.recordedAt >= :since
      and not exists (
        select 1 from NotificationDeliveryRecord d
        where d.tenantId = e.tenantId and d.certificateEventId = e.id and d.channel = :channel)
    order by e.recordedAt, e.id"""

private const val PLAN_CERTIFICATE = """
    insert into notification_deliveries
        (id, tenant_id, certificate_event_id, channel, status, next_attempt_at, created_at)
    values (:id, :tenant, :event, :channel, 'pending', :now, :now)
    on conflict (tenant_id, certificate_event_id, channel) do nothing"""

private const val DUE = """
    select new dev.sard.server.notify.DueDelivery(
        d.tenantId, d.id, d.runId, d.fireId, d.channel, d.attempts, d.createdAt, d.episodeId, d.episodeEvent,
        d.certificateEventId, e.serial, e.event)
    from NotificationDeliveryRecord d
    left join AgentCertificateEventRecord e on e.tenantId = d.tenantId and e.id = d.certificateEventId
    where d.status = 'pending' and d.nextAttemptAt <= :now and d.channel in :channels
    order by d.nextAttemptAt, d.id"""

private const val PENDING = "select count(*) from NotificationDeliveryRecord d where d.status = 'pending'"

private const val PLAN = """
    insert into notification_deliveries (id, tenant_id, run_id, channel, status, next_attempt_at, created_at)
    values (:id, :tenant, :run, :channel, 'pending', :now, :now)
    on conflict (tenant_id, run_id, channel) do nothing"""

private const val PLAN_ALERT = """
    insert into notification_deliveries (id, tenant_id, fire_id, channel, status, next_attempt_at, created_at)
    values (:id, :tenant, :fire, :channel, 'pending', :now, :now)
    on conflict (tenant_id, fire_id, channel) do nothing"""

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
    private val connected: AgentConnected = AgentConnected { false },
) {
    /** Finished runs since [since] without a delivery through [channel], oldest first. */
    fun unplanned(
        channel: String,
        since: Instant,
        limit: Int,
    ): List<Unplanned> = withoutDelivery(UNPLANNED, Unplanned::class.java, channel, since, limit)

    /** Alerts recorded since [since] without a delivery through [channel], oldest first. */
    fun unplannedAlerts(
        channel: String,
        since: Instant,
        limit: Int,
    ): List<UnplannedAlert> = withoutDelivery(UNPLANNED_ALERTS, UnplannedAlert::class.java, channel, since, limit)

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
    ): Int = planAll(tenantId, PLAN, "run", runs, channel, now)

    /** One pending delivery through [channel] for each alert of [fires] of [tenantId]; returns how many are new. */
    fun planAlerts(
        tenantId: UUID,
        fires: List<UUID>,
        channel: String,
        now: Instant,
    ): Int = planAll(tenantId, PLAN_ALERT, "fire", fires, channel, now)

    /**
     * Takes [delivery] for one attempt: it is not due again until [until], so a crash during the
     * send leads to another attempt then. Returns what to tell about, or null when another
     * sender took it first.
     */
    fun claim(
        delivery: DueDelivery,
        now: Instant,
        until: Instant,
    ): Claimed? =
        sessions.inTenant(delivery.tenantId) { session ->
            val claimed =
                session
                    .createNativeMutationQuery(CLAIM)
                    .setParameter("until", until)
                    .setParameter("tenant", delivery.tenantId)
                    .setParameter("id", delivery.id)
                    .setParameter("now", now)
                    .executeUpdate() == 1
            if (claimed) claimed(session, delivery, now) else null
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
                        closing(session, decision, delivery.attempts, now)
                    }
                }
            query.setParameter("tenant", delivery.tenantId).setParameter("id", delivery.id).executeUpdate() == 1
        }

    private fun <T> withoutDelivery(
        query: String,
        type: Class<T>,
        channel: String,
        since: Instant,
        limit: Int,
    ): List<T> =
        sessions.system { session ->
            session
                .createSelectionQuery(query, type)
                .setParameter("since", since)
                .setParameter("channel", channel)
                .setMaxResults(limit)
                .list()
        }

    /** Runs the insert [sql] once per subject, bound to [subjectParameter]; returns how many rows are new. */
    private fun planAll(
        tenantId: UUID,
        sql: String,
        subjectParameter: String,
        subjects: List<UUID>,
        channel: String,
        now: Instant,
    ): Int =
        sessions.inTenant(tenantId) { session ->
            subjects.sumOf { subject ->
                session
                    .createNativeMutationQuery(sql)
                    .setParameter("id", ids.next())
                    .setParameter("tenant", tenantId)
                    .setParameter(subjectParameter, subject)
                    .setParameter("channel", channel)
                    .setParameter("now", now)
                    .executeUpdate()
            }
        }

    private fun claimed(
        session: Session,
        delivery: DueDelivery,
        now: Instant,
    ): Claimed =
        when {
            delivery.fireId != null -> {
                Claimed.Alert(Notices.alert(session, delivery.tenantId, delivery.fireId))
            }

            delivery.episodeId != null -> {
                Claimed.Overdue(Notices.overdue(session, delivery.tenantId, delivery))
            }

            delivery.certificateEventId != null -> {
                Claimed.Certificate(Notices.certificate(session, delivery, now, connected))
            }

            else -> {
                Claimed.Finished(Notices.finished(session, delivery.tenantId, checkNotNull(delivery.runId)))
            }
        }
}

/**
 * The overdue episodes' side of the queue (F6, ADR 00XX-draft-source-freshness): finding episodes without a
 * delivery reads across tenants in the read-only system session (ADR 0013, caller 5); planning writes in the
 * episode's own tenant, guarded by the key (tenant, episode, event, channel).
 */
class OverdueDeliveries(
    private val sessions: TenantSessions,
    private val ids: UuidV7,
) {
    /** Episodes opened, or closed by a success, since [since] without a delivery through [channel]. */
    fun unplanned(
        channel: String,
        since: Instant,
        limit: Int,
    ): List<UnplannedEpisode> =
        sessions.system { session ->
            listOf(OPENED_EPISODES, CLOSED_EPISODES).flatMap { hql ->
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
                    .setParameter("event", episode.event)
                    .setParameter("channel", channel)
                    .setParameter("now", now)
                    .executeUpdate()
            }
        }
}

private fun closing(
    session: Session,
    decision: Decision,
    attempts: Int,
    now: Instant,
): MutationQuery {
    val closing = Closing.of(decision, attempts)
    return session
        .createNativeMutationQuery(CLOSE)
        .setParameter("status", closing.status)
        .setParameter("now", now)
        .setParameter("attempts", closing.attempts)
        .setParameter("error", closing.reason, String::class.java)
}

/** How a delivery ends: the status it closes with, the attempts counted, the last error if the end has one. */
private class Closing(
    val status: String,
    val attempts: Int,
    val reason: String?,
) {
    companion object {
        fun of(
            decision: Decision,
            attempts: Int,
        ): Closing =
            when (decision) {
                Decision.Delivered -> Closing("delivered", attempts, null)
                Decision.Skipped -> Closing("skipped", attempts, null)
                is Decision.Failed -> Closing("failed", decision.attempts, decision.reason)
                is Decision.Expired -> Closing("expired", decision.attempts, decision.reason)
                is Decision.Retry -> error("a retry keeps the delivery pending")
            }
    }
}

/** The certificate events' side of the queue (C1 phase 3): finding events without a delivery, planning one. */
class CertificateDeliveries(
    private val sessions: TenantSessions,
    private val ids: UuidV7,
) {
    /** Events recorded since [since] without a delivery through [channel], oldest first. */
    fun unplanned(
        channel: String,
        since: Instant,
        limit: Int,
    ): List<UnplannedCertificateEvent> =
        sessions.system { session ->
            session
                .createSelectionQuery(UNPLANNED_CERTIFICATES, UnplannedCertificateEvent::class.java)
                .setParameter("since", since)
                .setParameter("channel", channel)
                .setMaxResults(limit)
                .list()
        }

    /** One pending delivery through [channel] for each of [events] of [tenantId]; returns how many are new. */
    fun plan(
        tenantId: UUID,
        events: List<UUID>,
        channel: String,
        now: Instant,
    ): Int =
        sessions.inTenant(tenantId) { session ->
            events.sumOf { event ->
                session
                    .createNativeMutationQuery(PLAN_CERTIFICATE)
                    .setParameter("id", ids.next())
                    .setParameter("tenant", tenantId)
                    .setParameter("event", event)
                    .setParameter("channel", channel)
                    .setParameter("now", now)
                    .executeUpdate()
            }
        }
}
