// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.persistence.HibernateTenantBridge
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.runs.StepsQueued
import dev.sard.server.scheduler.CronSchedule
import dev.sard.server.scheduler.Fires
import org.hibernate.Session
import org.slf4j.LoggerFactory
import org.springframework.context.SmartLifecycle
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

private val log = LoggerFactory.getLogger(VerificationLoop::class.java)

private const val TENANTS = "select id, true from tenants order by id"
private const val SCHEDULE = "select cron, timezone, updated_at from verification_schedule"

/** The furthest back a cycle moment is looked for: a monthly cron's. */
private val LOOK_BACK: Duration = Duration.ofDays(32)

/**
 * One tick of restore verification (V4) per tenant, in one transaction of that tenant: settle the attempts that
 * ended, hand out queued jobs (or expire them), make the jobs of the cycle's current moment and hand them out. The
 * tenants and the installation's schedule are read across tenants (ADR 0013, system callers). Jobs are locked
 * SKIP LOCKED and a cycle job is made only for a source without one since the moment, so a second server neither
 * takes the same job nor makes a second one. Verifiers that got a step are woken after the commit.
 */
class VerificationLoop(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val cycles: VerificationCycles,
    private val queue: VerificationQueue,
    private val settlement: VerificationSettlement,
    private val queued: StepsQueued,
    private val settings: VerificationSettings,
) {
    fun tick() {
        val now = clock.instant()
        val (tenants, schedule) = sessions.system { session -> tenants(session) to schedule(session) }
        val moments = CycleMoments.of(schedule.fires, maxOf(schedule.since, now - LOOK_BACK), now)
        for (tenantId in tenants.filter { it != HibernateTenantBridge.SYSTEM_TENANT_ID }) {
            runCatching { tick(tenantId, moments, now) }
                .onFailure { log.warn("verification of tenant {} waits for the next tick", tenantId, it) }
        }
    }

    /** Settles ended attempts first, so a job retried now is handed out in the same tick. */
    private fun tick(
        tenantId: UUID,
        moments: CycleMoments,
        now: Instant,
    ) {
        val woken =
            sessions.inTenant(tenantId) { session ->
                settlement.settle(session, tenantId, now)
                val before = queue.assign(session, tenantId, now)
                val made = cycles.fire(session, tenantId, moments, now)
                before + if (made > 0) queue.assign(session, tenantId, now) else emptySet()
            }
        for (verifier in woken) queued.onQueued(tenantId, verifier)
    }

    private fun tenants(session: Session): List<UUID> =
        session.createNativeQuery(TENANTS, Array<Any?>::class.java).list().map { it[0] as UUID }

    /** The installation's schedule; without a row, the default since ever (V-D9). */
    private fun schedule(session: Session): CycleSchedule {
        val row = session.createNativeQuery(SCHEDULE, Array<Any?>::class.java).list().firstOrNull()
        val (cron, zone, since) = row ?: arrayOf(settings.defaultCron, settings.defaultTimezone, Instant.EPOCH)
        return CycleSchedule(CronSchedule.parse(cron as String, zone as String), since as Instant)
    }
}

/** The cron of the cycle and when it was set: moments before that do not count. */
private class CycleSchedule(
    val fires: Fires,
    val since: Instant,
)

/** Runs [tick] every [interval] on one thread; ticks never overlap and a failing one never stops the next. */
class VerificationTicker(
    private val interval: Duration,
    private val tick: () -> Unit,
) : SmartLifecycle {
    @Volatile
    private var executor: ScheduledExecutorService? = null

    override fun start() {
        val scheduler =
            Executors.newSingleThreadScheduledExecutor { Thread(it, "sard-verification").apply { isDaemon = true } }
        scheduler.scheduleWithFixedDelay(::safeTick, interval.toMillis(), interval.toMillis(), TimeUnit.MILLISECONDS)
        executor = scheduler
    }

    override fun stop() {
        executor?.shutdownNow()
        executor = null
    }

    override fun isRunning() = executor != null

    private fun safeTick() {
        runCatching(tick).onFailure { log.warn("Verification tick failed; retrying at the next one", it) }
    }
}
