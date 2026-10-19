// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.persistence.UuidV7
import dev.sard.server.scheduler.Fires
import org.hibernate.Session
import java.time.Instant
import java.util.UUID

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). The latest whole snapshot of each live source whose
// verification is on (V-D9): partial and forgotten ones prove nothing; a source without one has nothing to
// verify yet. With :cycle set, only sources without a cycle job since that moment.
private const val LATEST = """
    select s.id, s.plugin, sn.id, sn.repository_id from sources s
    join lateral (
        select id, repository_id from snapshots
        where tenant_id = s.tenant_id and source_id = s.id and not partial and forgotten_at is null
        order by created_at desc, id desc limit 1) sn on true
    where s.tenant_id = :tenant and s.deleted_at is null and s.verification_enabled
    and not exists (
        select 1 from verification_jobs j
        where j.tenant_id = s.tenant_id and j.source_id = s.id and j.reason = 'cycle' and j.created_at >= :cycle)
    order by s.id"""

// At most one open job per source (V-D9): the partial unique index refuses a second, the insert does nothing.
private const val ENQUEUE = """
    insert into verification_jobs (id, tenant_id, source_id, snapshot_ref, repository_id, plugin, reason, status,
        created_at, deadline)
    values (:id, :tenant, :source, :snapshot, :repository, :plugin, :reason, 'queued', :now, :deadline)
    on conflict do nothing"""

/** Why a job was made; [stored] is verification_jobs.reason. */
enum class JobReason(
    val stored: String,
) {
    CYCLE("cycle"),
    MANUAL("manual"),
}

/** The cycle's moment at or before now ([last], null when none since the schedule changed) and the next one. */
data class CycleMoments(
    val last: Instant?,
    val next: Instant,
) {
    /** These moments still hold at [now]: the next one has not come. */
    fun currentAt(now: Instant): Boolean = now < next

    companion object {
        /** The moments of [fires] after [since] (the schedule's change, or a bound in the past) around [now]. */
        fun of(
            fires: Fires,
            since: Instant,
            now: Instant,
        ): CycleMoments {
            var last: Instant? = null
            var next = fires.nextAfter(since)
            while (next <= now) {
                last = next
                next = fires.nextAfter(next)
            }
            return CycleMoments(last, next)
        }
    }
}

/** A row of LATEST: source, plugin, snapshot, repository. */
private fun jobSnapshotOf(row: Array<Any?>): JobSnapshot {
    val (source, plugin, snapshot) = row
    return JobSnapshot(VerifiedSnapshot(source as UUID, snapshot as UUID, row.last() as String), plugin as String)
}

/** A snapshot to verify and the plugin of its source. */
data class JobSnapshot(
    val snapshot: VerifiedSnapshot,
    val plugin: String,
)

/**
 * The daily cycle (V-D9) in a tenant: at the installation's cron moment, a job for the latest whole snapshot of
 * every source whose verification is on and that has had no cycle job since that moment, unless it has an open
 * one. The same snapshot is verified again when there is no newer one: that catches a repository gone bad. A
 * tenant without any verifier gets no jobs and no rows at all: «no verifier» is its state, not a record (owner
 * decision 7, no spam by construction). A job waits for a verifier until the next moment.
 */
class VerificationCycles(
    private val ids: UuidV7,
    private val pool: VerifierPool,
) {
    /** Makes the cycle's jobs of [moments] in the tenant; returns how many. */
    fun fire(
        session: Session,
        tenantId: UUID,
        moments: CycleMoments,
        now: Instant,
    ): Int {
        val cycle = moments.last?.takeIf { pool.any(session, tenantId) } ?: return 0
        return latest(session, tenantId, cycle).count {
            enqueue(session, tenantId, it, JobReason.CYCLE, now, moments.next)
        }
    }

    /** Makes a job for [snapshot] unless its source has an open one; true when it made it. */
    fun enqueue(
        session: Session,
        tenantId: UUID,
        snapshot: JobSnapshot,
        reason: JobReason,
        now: Instant,
        deadline: Instant,
    ): Boolean =
        session
            .createNativeMutationQuery(ENQUEUE.trimIndent())
            .setParameter("id", ids.next())
            .setParameter("tenant", tenantId)
            .setParameter("source", snapshot.snapshot.sourceId)
            .setParameter("snapshot", snapshot.snapshot.snapshotRef)
            .setParameter("repository", snapshot.snapshot.repositoryId)
            .setParameter("plugin", snapshot.plugin)
            .setParameter("reason", reason.stored)
            .setParameter("now", now)
            .setParameter("deadline", deadline)
            .executeUpdate() == 1

    private fun latest(
        session: Session,
        tenantId: UUID,
        cycle: Instant,
    ): List<JobSnapshot> =
        session
            .createNativeQuery(LATEST.trimIndent(), Array<Any?>::class.java)
            .setParameter("tenant", tenantId)
            .setParameter("cycle", cycle)
            .list()
            .map(::jobSnapshotOf)
}
