// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.runs.SourceNotFound
import org.hibernate.Session
import java.time.Clock
import java.time.Instant
import java.util.UUID

// Native SQL names tenant_id explicitly (ADR 0013, rule 8).
private const val SOURCE =
    "select verification_enabled, plugin, true from sources where tenant_id = :tenant and id = :id and deleted_at is null"

/** The last three columns of SUMMARIES: last pass, its level, open stale episode. */
private const val SUMMARY_TAIL = 3
private const val ENABLE =
    "update sources set verification_enabled = :enabled where tenant_id = :tenant and id = :id and deleted_at is null"

// The last record of each source, and its last pass: the state the console shows (V4, for V6).
private const val SUMMARIES = """
    select s.id, s.verification_enabled,
        (select rv.status from restore_verifications rv where rv.tenant_id = s.tenant_id and rv.source_id = s.id
            order by rv.verified_at desc, rv.id desc limit 1),
        (select rv.verified_at from restore_verifications rv
            where rv.tenant_id = s.tenant_id and rv.source_id = s.id and rv.status = 'passed'
            order by rv.verified_at desc, rv.id desc limit 1),
        (select rv.level from restore_verifications rv
            where rv.tenant_id = s.tenant_id and rv.source_id = s.id and rv.status = 'passed'
            order by rv.verified_at desc, rv.id desc limit 1),
        (select e.counted_from from verification_stale_episodes e
            where e.tenant_id = s.tenant_id and e.source_id = s.id and e.closed_at is null)
    from sources s where s.tenant_id = :tenant and s.id in (:ids)"""
private const val OPEN_JOB = """
    select id, status, reason, created_at, deadline, failed_attempts, cast(unsuitable as text) from verification_jobs
    where tenant_id = :tenant and source_id = :source and status <> 'closed'"""

// The snapshot «verify now» takes: the given one of the source, or its latest whole one.
private const val SNAPSHOT = """
    select id, repository_id, partial or forgotten_at is not null from snapshots
    where tenant_id = :tenant and source_id = :source and (cast(:snapshot as uuid) is null or id = :snapshot)
    and (cast(:snapshot as uuid) is not null or (not partial and forgotten_at is null))
    order by created_at desc, id desc limit 1"""

// /status: the oldest of the last passes of the live sources whose verification is on and that have a whole
// snapshot to verify; null while one of them has none (owner decision 5: one fresh source must not hide another).
private const val OLDEST_LAST_PASS = """
    select count(*), count(pass.at), min(pass.at) from sources s
    left join lateral (select max(rv.verified_at) as at from restore_verifications rv
        where rv.tenant_id = s.tenant_id and rv.source_id = s.id and rv.status = 'passed') pass on true
    where s.tenant_id = :tenant and s.deleted_at is null and s.verification_enabled
    and exists (select 1 from snapshots sn where sn.tenant_id = s.tenant_id and sn.source_id = s.id and not sn.partial)"""

/** The verification state of a source as the console shows it. */
data class VerificationSummary(
    val enabled: Boolean,
    /** The last verification's status; no_verifier while the tenant has none at all; null before the first. */
    val status: VerificationStatus?,
    val lastVerifiedAt: Instant?,
    val lastVerifiedLevel: Int?,
    /** Not verified successfully for longer than N days since this moment; null otherwise. */
    val staleSince: Instant?,
)

/** A job that is queued or running. */
data class OpenJob(
    val id: UUID,
    val running: Boolean,
    val reason: JobReason,
    val createdAt: Instant,
    val deadline: Instant,
    val failedAttempts: Int,
    /** Why each verifier did not fit at the last try, as stored JSON; null while none was tried. */
    val unsuitable: String?,
)

/** A source's verification: its summary and its open job. */
data class SourceVerification(
    val summary: VerificationSummary,
    val openJob: OpenJob?,
)

/** «Verify now» while the source has an open job: 409 with that job. */
class VerificationActive(
    val jobId: UUID,
) : RuntimeException("verification job $jobId is open")

/** «Verify now» of a snapshot that is not the source's, or not whole. */
class SnapshotUnusable(
    message: String,
) : RuntimeException(message)

/**
 * What REST does with restore verification in a tenant (V4): a source's state, turning it on or off, «verify now»
 * of the latest whole snapshot or a given one, and the /status figure. Every call is one transaction of the tenant.
 */
class Verifications(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val cycles: VerificationCycles,
    private val pool: VerifierPool,
    private val schedule: VerificationSchedule,
) {
    fun state(
        tenantId: UUID,
        sourceId: UUID,
    ): SourceVerification =
        sessions.inTenant(tenantId) { session ->
            requireSource(session, tenantId, sourceId)
            val summary = summaries(session, tenantId, listOf(sourceId)).getValue(sourceId)
            SourceVerification(summary, openJob(session, tenantId, sourceId))
        }

    /** Summaries of [sourceIds]; a deleted or unknown id is absent. */
    fun summaries(
        tenantId: UUID,
        sourceIds: List<UUID>,
    ): Map<UUID, VerificationSummary> =
        if (sourceIds.isEmpty()) emptyMap() else sessions.inTenant(tenantId) { summaries(it, tenantId, sourceIds) }

    fun setEnabled(
        tenantId: UUID,
        sourceId: UUID,
        enabled: Boolean,
    ): SourceVerification {
        val changed =
            sessions.inTenant(tenantId) { session ->
                session
                    .createNativeMutationQuery(ENABLE)
                    .setParameter("enabled", enabled)
                    .setParameter("tenant", tenantId)
                    .setParameter("id", sourceId)
                    .executeUpdate()
            }
        if (changed == 0) throw SourceNotFound(sourceId)
        return state(tenantId, sourceId)
    }

    /**
     * Queues a manual job for [snapshotRef] (snapshots.id) or, when null, the source's latest whole snapshot; it waits
     * for a verifier until the cycle's next moment, as a cycle job does. Throws [VerificationActive] while the source
     * has an open job and [SnapshotUnusable] for a snapshot that is not the source's or not whole.
     */
    fun verifyNow(
        tenantId: UUID,
        sourceId: UUID,
        snapshotRef: UUID?,
    ): OpenJob =
        sessions.inTenant(tenantId) { session ->
            val plugin = requireSource(session, tenantId, sourceId)
            openJob(session, tenantId, sourceId)?.let { throw VerificationActive(it.id) }
            val snapshot = snapshot(session, tenantId, sourceId, snapshotRef)
            val now = clock.instant()
            val deadline = schedule.moments(session, now).next
            val made = cycles.enqueue(session, tenantId, JobSnapshot(snapshot, plugin), JobReason.MANUAL, now, deadline)
            if (!made) throw VerificationActive(checkNotNull(openJob(session, tenantId, sourceId)).id)
            checkNotNull(openJob(session, tenantId, sourceId))
        }

    /** The oldest last pass over the tenant's verified sources; null if one has none or there is none to verify. */
    fun lastVerifiedAt(tenantId: UUID): Instant? =
        sessions.inTenant(tenantId) { session ->
            val row =
                session
                    .createNativeQuery(OLDEST_LAST_PASS.trimIndent(), Array<Any?>::class.java)
                    .setParameter("tenant", tenantId)
                    .singleResult
            val (sources, passed, oldest) = row
            (oldest as Instant?).takeIf { (sources as Number).toLong() > 0 && sources == passed }
        }

    private fun summaries(
        session: Session,
        tenantId: UUID,
        sourceIds: List<UUID>,
    ): Map<UUID, VerificationSummary> {
        val noVerifier = !pool.any(session, tenantId)
        return session
            .createNativeQuery(SUMMARIES.trimIndent(), Array<Any?>::class.java)
            .setParameter("tenant", tenantId)
            .setParameterList("ids", sourceIds)
            .list()
            .associate { row -> row[0] as UUID to summaryOf(row, noVerifier) }
    }

    private fun summaryOf(
        row: Array<Any?>,
        noVerifier: Boolean,
    ): VerificationSummary {
        val (_, enabled, last) = row
        val status =
            if (enabled == true &&
                noVerifier
            ) {
                VerificationStatus.NO_VERIFIER
            } else {
                last?.let { VerificationStatus.of(it as String) }
            }
        val (passedAt, passedLevel, staleSince) = row.takeLast(SUMMARY_TAIL)
        return VerificationSummary(
            enabled as Boolean,
            status,
            passedAt as Instant?,
            (passedLevel as Number?)?.toInt(),
            staleSince as Instant?,
        )
    }

    private fun openJob(
        session: Session,
        tenantId: UUID,
        sourceId: UUID,
    ): OpenJob? =
        session
            .createNativeQuery(OPEN_JOB.trimIndent(), Array<Any?>::class.java)
            .setParameter("tenant", tenantId)
            .setParameter("source", sourceId)
            .list()
            .firstOrNull()
            ?.let { row ->
                OpenJob(
                    id = row[0] as UUID,
                    running = row[1] == "running",
                    reason = JobReason.entries.single { it.stored == row[2] },
                    createdAt = row[3] as Instant,
                    deadline = row[4] as Instant,
                    failedAttempts = (row[5] as Number).toInt(),
                    unsuitable = row[6] as String?,
                )
            }

    /** The live source's plugin; [SourceNotFound] otherwise. */
    private fun requireSource(
        session: Session,
        tenantId: UUID,
        sourceId: UUID,
    ): String =
        session
            .createNativeQuery(SOURCE, Array<Any?>::class.java)
            .setParameter("tenant", tenantId)
            .setParameter("id", sourceId)
            .list()
            .firstOrNull()
            ?.let { it[1] as String } ?: throw SourceNotFound(sourceId)

    private fun snapshot(
        session: Session,
        tenantId: UUID,
        sourceId: UUID,
        snapshotRef: UUID?,
    ): VerifiedSnapshot {
        val row =
            session
                .createNativeQuery(SNAPSHOT.trimIndent(), Array<Any?>::class.java)
                .setParameter("tenant", tenantId)
                .setParameter("source", sourceId)
                .setParameter("snapshot", snapshotRef, UUID::class.java)
                .list()
                .firstOrNull() ?: throw SnapshotUnusable("the source has no such snapshot to verify")
        if (row[2] == true) throw SnapshotUnusable("the snapshot is partial or forgotten")
        return VerifiedSnapshot(sourceId, row[0] as UUID, row[1] as String)
    }
}
