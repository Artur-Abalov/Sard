// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.RunRecord
import dev.sard.server.persistence.RunStepRecord
import dev.sard.server.persistence.ScheduleFireRecord
import dev.sard.server.persistence.ScheduleRecord
import dev.sard.server.persistence.SourceRecord
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.runs.RunState
import dev.sard.server.runs.RunViews
import dev.sard.server.runs.Trigger
import dev.sard.server.scheduler.FireOutcome
import dev.sard.server.scheduler.FireReason
import org.hibernate.Session
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

/** A pending delivery whose time has come: about a run or, exactly one of the two, a schedule fire. */
data class DueDelivery(
    val tenantId: UUID,
    val id: UUID,
    val runId: UUID?,
    val fireId: UUID?,
    val channel: String,
    val attempts: Int,
    val createdAt: Instant,
) {
    /** What the delivery is about, as the log names it. */
    val subject: String get() = if (fireId != null) "schedule fire $fireId" else "run $runId"
}

/** What a claimed delivery tells about, read back at send time. */
sealed interface Claimed {
    data class Finished(
        val notice: RunNotice,
    ) : Claimed

    data class Alert(
        val notice: SkipAlertNotice,
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

private const val DUE = """
    select new dev.sard.server.notify.DueDelivery(
        d.tenantId, d.id, d.runId, d.fireId, d.channel, d.attempts, d.createdAt)
    from NotificationDeliveryRecord d
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

    /** Alerts recorded since [since] without a delivery through [channel], oldest first. */
    fun unplannedAlerts(
        channel: String,
        since: Instant,
        limit: Int,
    ): List<UnplannedAlert> =
        sessions.system { session ->
            session
                .createSelectionQuery(UNPLANNED_ALERTS, UnplannedAlert::class.java)
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

    /** One pending delivery through [channel] for each alert of [fires] of [tenantId]; returns how many are new. */
    fun planAlerts(
        tenantId: UUID,
        fires: List<UUID>,
        channel: String,
        now: Instant,
    ): Int =
        sessions.inTenant(tenantId) { session ->
            fires.sumOf { fire ->
                session
                    .createNativeMutationQuery(PLAN_ALERT)
                    .setParameter("id", ids.next())
                    .setParameter("tenant", tenantId)
                    .setParameter("fire", fire)
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
            if (claimed) claimed(session, delivery) else null
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

    private fun claimed(
        session: Session,
        delivery: DueDelivery,
    ): Claimed =
        if (delivery.fireId != null) {
            Claimed.Alert(Notices.alert(session, delivery.tenantId, delivery.fireId))
        } else {
            Claimed.Finished(Notices.finished(session, delivery.tenantId, checkNotNull(delivery.runId)))
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
