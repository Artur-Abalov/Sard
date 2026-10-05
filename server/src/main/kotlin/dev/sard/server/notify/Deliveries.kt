// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.RunRecord
import dev.sard.server.persistence.RunStepRecord
import dev.sard.server.persistence.SourceRecord
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.runs.RunState
import dev.sard.server.runs.RunViews
import dev.sard.server.runs.Trigger
import org.hibernate.Session
import java.time.Instant
import java.util.UUID

/** A finished run that has no delivery through a channel yet. */
data class Unplanned(
    val tenantId: UUID,
    val runId: UUID,
)

/** A pending delivery whose time has come. */
data class DueDelivery(
    val tenantId: UUID,
    val id: UUID,
    val runId: UUID,
    val channel: String,
    val attempts: Int,
    val createdAt: Instant,
)

private const val UNPLANNED = """
    select new dev.sard.server.notify.Unplanned(r.tenantId, r.id) from RunRecord r
    where r.finishedAt >= :since
      and not exists (
        select 1 from NotificationDeliveryRecord d
        where d.tenantId = r.tenantId and d.runId = r.id and d.channel = :channel)
    order by r.finishedAt, r.id"""

private const val DUE = """
    select new dev.sard.server.notify.DueDelivery(d.tenantId, d.id, d.runId, d.channel, d.attempts, d.createdAt)
    from NotificationDeliveryRecord d
    where d.status = 'pending' and d.nextAttemptAt <= :now and d.channel in :channels
    order by d.nextAttemptAt, d.id"""

private const val PENDING = "select count(*) from NotificationDeliveryRecord d where d.status = 'pending'"

private const val PLAN = """
    insert into notification_deliveries (id, tenant_id, run_id, channel, status, next_attempt_at, created_at)
    values (:id, :tenant, :run, :channel, 'pending', :now, :now)
    on conflict (tenant_id, run_id, channel) do nothing"""

private const val CLAIM = """
    update notification_deliveries set next_attempt_at = :until
    where tenant_id = :tenant and id = :id and status = 'pending' and next_attempt_at <= :now"""

private const val FIRST_STEP = "from RunStepRecord s where s.runId = :run order by s.ordinal"

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
     * send leads to another attempt then. Returns the run to tell about, or null when another
     * sender took it first.
     */
    fun claim(
        delivery: DueDelivery,
        now: Instant,
        until: Instant,
    ): RunNotice? =
        sessions.inTenant(delivery.tenantId) { session ->
            val claimed =
                session
                    .createNativeMutationQuery(CLAIM)
                    .setParameter("until", until)
                    .setParameter("tenant", delivery.tenantId)
                    .setParameter("id", delivery.id)
                    .setParameter("now", now)
                    .executeUpdate() == 1
            if (claimed) notice(session, delivery.tenantId, delivery.runId) else null
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

    private fun notice(
        session: Session,
        tenantId: UUID,
        runId: UUID,
    ): RunNotice {
        val run = session.find(RunRecord::class.java, runId)
        val source = session.find(SourceRecord::class.java, run.sourceId)
        val step =
            session
                .createSelectionQuery(FIRST_STEP, RunStepRecord::class.java)
                .setParameter("run", runId)
                .setMaxResults(1)
                .singleResult
        val agentId = step.agentId
        val agent = session.find(Agent::class.java, agentId)
        val stepView = RunViews.step(step)
        return RunNotice(
            tenantId = tenantId,
            runId = runId,
            trigger = Trigger.of(run.trigger),
            status = RunState.of(run.status),
            message = run.message,
            queuedAt = run.queuedAt,
            finishedAt = checkNotNull(run.finishedAt) { "run $runId is not finished" },
            sourceId = source.id,
            sourceName = source.name,
            agentId = agentId,
            agentHostname = agent.hostname,
            stepStatus = stepView.status,
            startedAt = run.startedAt,
            backup = stepView.backup?.let { BackupSizes(it.totalBytes, it.addedBytes) },
        )
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
