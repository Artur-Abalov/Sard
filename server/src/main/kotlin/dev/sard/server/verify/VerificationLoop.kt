// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.loop.PeriodicTask
import dev.sard.server.loop.Timing
import dev.sard.server.persistence.HibernateTenantBridge
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.runs.StepsQueued
import org.hibernate.Session
import org.slf4j.LoggerFactory
import org.springframework.context.SmartLifecycle
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID

private val log = LoggerFactory.getLogger(VerificationLoop::class.java)

private const val TENANTS = "select id, true from tenants order by id"

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
    private val schedule: VerificationScheduleReader,
    private val stale: StaleEpisodes,
    private val noVerifier: NoVerifierEpisodes,
) {
    fun tick() {
        val now = clock.instant()
        val (tenants, cycle) = sessions.system { session -> tenants(session) to schedule.current(session) }
        val moments = VerificationScheduleReader.moments(cycle, now)
        for (tenantId in tenants.filter { it != HibernateTenantBridge.SYSTEM_TENANT_ID }) {
            runCatching { tick(tenantId, moments, cycle, now) }
                .onFailure { log.warn("verification of tenant {} waits for the next tick", tenantId, it) }
        }
    }

    /** Settles ended attempts first, so a job retried now is handed out in the same tick. */
    private fun tick(
        tenantId: UUID,
        moments: CycleMoments,
        cycle: CycleSchedule,
        now: Instant,
    ) {
        val woken =
            sessions.inTenant(tenantId) { session ->
                settlement.settle(session, tenantId, now)
                stale.update(session, tenantId, now, cycle)
                noVerifier.update(session, tenantId, now)
                val before = queue.assign(session, tenantId, now)
                val made = cycles.fire(session, tenantId, moments, now)
                before + if (made > 0) queue.assign(session, tenantId, now) else emptySet()
            }
        for (verifier in woken) queued.onQueued(tenantId, verifier)
    }

    private fun tenants(session: Session): List<UUID> =
        session.createNativeQuery(TENANTS, Array<Any?>::class.java).list().map { it[0] as UUID }
}

/** Runs [tick] every [interval] on one thread; ticks never overlap and a failing one never stops the next. */
class VerificationTicker(
    interval: Duration,
    tick: () -> Unit,
) : SmartLifecycle by PeriodicTask(
        name = "sard-verification",
        timing = Timing(interval),
        onFailure = { log.warn("Verification tick failed; retrying at the next one", it) },
        action = tick,
    )
