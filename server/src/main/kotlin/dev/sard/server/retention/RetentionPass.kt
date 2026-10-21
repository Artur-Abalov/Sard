// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import dev.sard.server.persistence.HibernateTenantBridge
import dev.sard.server.persistence.TenantSessions
import org.hibernate.Session
import org.slf4j.LoggerFactory
import java.time.Clock
import java.time.Instant
import java.util.EnumMap
import java.util.UUID

private val log = LoggerFactory.getLogger(RetentionPass::class.java)

private const val TENANTS = "select id from tenants"

/** What a pass deleted, by kind. */
class RetentionTotals {
    private val counts = EnumMap<RetentionKind, Long>(RetentionKind::class.java)

    operator fun get(kind: RetentionKind): Long = counts[kind] ?: 0

    fun add(
        kind: RetentionKind,
        count: Int,
    ) {
        counts[kind] = get(kind) + count
    }

    /** The one line the server's log gets for a pass. */
    fun summary(): String {
        val counts = RetentionKind.entries.map { "${it.tag}=${get(it)}" }
        return counts.joinToString(" ", "Retention pass deleted ")
    }
}

/**
 * One pass of the cleaning (H1 phase 2): everything older than its term, bottom up along the foreign keys (log
 * partitions, deliveries, schedule fires, verifications with their jobs, run steps with their runs), batch by batch,
 * until nothing eligible is left. The tenants are read across tenants (ADR 0013, system callers) and each batch is
 * one transaction of the tenant of its rows. Rows nothing may delete, and the rules that keep them, are in the SQL.
 */
class RetentionPass(
    private val sessions: TenantSessions,
    private val logs: StepLogRetention,
    private val clock: Clock,
    private val settings: RetentionSettings,
    private val metrics: RetentionMetrics,
) {
    fun run(): RetentionTotals {
        val now = clock.instant()
        val totals = RetentionTotals()
        dropLogPartitions(now, totals)
        val tenants = sessions.system { session -> tenants(session) }
        for (tenantId in tenants.filter { it != HibernateTenantBridge.SYSTEM_TENANT_ID }) {
            runCatching { clean(tenantId, now, totals) }
                .onFailure {
                    log.error("Retention of tenant {} stopped by {}; again next pass", tenantId, it.javaClass.name)
                }
        }
        log.info(totals.summary())
        return totals
    }

    private fun dropLogPartitions(
        now: Instant,
        totals: RetentionTotals,
    ) {
        for (name in logs.expired(now.minus(settings.stepLogs))) {
            runCatching { logs.drop(name) }
                .onSuccess { dropped -> if (dropped) count(RetentionKind.STEP_LOG_PARTITION, 1, totals) }
                .onFailure { log.error("Retention could not drop log partition {}: {}", name, it.javaClass.name) }
        }
    }

    private fun clean(
        tenantId: UUID,
        now: Instant,
        totals: RetentionTotals,
    ) {
        val deliveries = now.minus(settings.notificationDeliveries)
        val fires = now.minus(settings.scheduleFires)
        val verifications = now.minus(settings.verifications)
        val runs = now.minus(settings.runs)
        batches(tenantId, totals, RetentionKind.NOTIFICATION_DELIVERY) { delete(it, DELIVERIES, tenantId, deliveries) }
        batches(tenantId, totals, RetentionKind.SCHEDULE_FIRE) { delete(it, DOWNTIME_FIRES, tenantId, fires) }
        batches(tenantId, totals, RetentionKind.SCHEDULE_FIRE) { delete(it, OTHER_FIRES, tenantId, fires) }
        batches(tenantId, totals, RetentionKind.RESTORE_VERIFICATION, RetentionKind.VERIFICATION_JOB) {
            deleteWith(it, VERIFICATIONS, tenantId, verifications)
        }
        batches(tenantId, totals, RetentionKind.RUN, RetentionKind.RUN_STEP) { deleteWith(it, RUNS, tenantId, runs) }
    }

    /**
     * Repeats [batch], each time in a transaction of the tenant, while a batch is full. [kinds] are what the numbers
     * a batch returns count, the first one being the rows the batch is limited by.
     */
    private fun batches(
        tenantId: UUID,
        totals: RetentionTotals,
        vararg kinds: RetentionKind,
        batch: (Session) -> List<Int>,
    ) {
        do {
            val deleted = sessions.inTenant(tenantId, batch)
            for ((kind, rows) in kinds.zip(deleted)) if (rows > 0) count(kind, rows, totals)
            val full = deleted.first() >= settings.batchSize
        } while (full && !Thread.currentThread().isInterrupted)
    }

    private fun count(
        kind: RetentionKind,
        count: Int,
        totals: RetentionTotals,
    ) {
        totals.add(kind, count)
        metrics.deleted(kind, count)
    }

    private fun delete(
        session: Session,
        sql: String,
        tenantId: UUID,
        cutoff: Instant,
    ): List<Int> =
        listOf(
            session
                .createNativeMutationQuery(sql)
                .setParameter("tenant", tenantId)
                .setParameter("cutoff", cutoff)
                .setParameter("limit", settings.batchSize)
                .executeUpdate(),
        )

    private fun deleteWith(
        session: Session,
        sql: String,
        tenantId: UUID,
        cutoff: Instant,
    ): List<Int> {
        val counts =
            session
                .createNativeQuery(sql, Array<Any?>::class.java)
                .setParameter("tenant", tenantId)
                .setParameter("cutoff", cutoff)
                .setParameter("limit", settings.batchSize)
                .singleResult
        return counts.map { (it as Number).toInt() }
    }

    private fun tenants(session: Session): List<UUID> = session.createNativeQuery(TENANTS, UUID::class.java).list()
}
