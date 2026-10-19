// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.runs.VerificationAttempt
import dev.sard.server.runs.VerificationRuns
import dev.sard.server.runs.VerifyTask
import org.hibernate.Session
import java.time.Instant
import java.util.UUID

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). The machines of the source's agent and of the agent
// that made the snapshot are both the source's (V-D7). Another server's jobs are skipped, not waited for.
private const val QUEUED = """
    select j.id, j.source_id, j.snapshot_ref, j.repository_id, j.plugin, j.fingerprint, j.fingerprint_sha256,
        j.deadline, sn.snapshot_id, sn.total_bytes, cast(src.config as text), src.deleted_at is not null,
        sa.machine_id, na.machine_id
    from verification_jobs j
    join snapshots sn on sn.tenant_id = j.tenant_id and sn.id = j.snapshot_ref
    join sources src on src.tenant_id = j.tenant_id and src.id = j.source_id
    join agents sa on sa.tenant_id = j.tenant_id and sa.id = src.agent_id
    join agents na on na.tenant_id = j.tenant_id and na.id = sn.agent_id
    where j.tenant_id = :tenant and j.status = 'queued'
    order by j.created_at, j.id
    for update of j skip locked"""
private const val RUNNING =
    "update verification_jobs set status = 'running', run_id = :run, verifier_agent_id = :verifier, " +
        "unsuitable = null where tenant_id = :tenant and id = :id and status = 'queued'"
private const val WAITING =
    "update verification_jobs set unsuitable = cast(:unsuitable as jsonb) where tenant_id = :tenant and id = :id"
private const val NO_VERIFIER_AT_ALL = "the tenant has no verifier"
internal const val CLOSE =
    "update verification_jobs set status = 'closed', closed_at = :now where tenant_id = :tenant and id = :id"

/** A queued job as the queue reads it. */
private class QueuedJob(
    row: Array<Any?>,
) {
    val id = row[0] as UUID
    val snapshot = VerifiedSnapshot(row[1] as UUID, row[2] as UUID, row[3] as String)
    val plugin = row[4] as String
    val fingerprint = row[5] as ByteArray?
    val fingerprintSha256 = row[6] as String?
    val deadline = row[7] as Instant
    val resticSnapshot = row[8] as String
    val totalBytes = (row[9] as Number).toLong()
    val config = row[10] as String
    val sourceDeleted = row[11] as Boolean
    val sourceMachines = setOfNotNull(row[12] as String?, row[13] as String?)
}

/**
 * Hands queued jobs to verifiers (V4 «Выдача», V-D8): each to the verifier [VerifierChoice] picks, as a run with one
 * VERIFY step that the dispatcher sends over the agent's Connect stream. A job nobody fits waits, keeping why, until
 * its deadline; then it is recorded as «no verifier» with those reasons. A deleted source's job closes unrecorded.
 */
class VerificationQueue(
    private val settings: VerificationSettings,
    private val pool: VerifierPool,
    private val runs: VerificationRuns,
    private val history: VerificationHistory,
) {
    /** Returns the verifiers that got a step, to wake once the caller's transaction commits. */
    fun assign(
        session: Session,
        tenantId: UUID,
        now: Instant,
    ): Set<UUID> {
        val jobs = queued(session, tenantId)
        if (jobs.isEmpty()) return emptySet()
        var candidates = pool.candidates(session, tenantId, now - settings.recentWindow)
        val woken = mutableSetOf<UUID>()
        for (job in jobs) {
            val taken = handle(session, tenantId, job, candidates, now) ?: continue
            woken += taken
            candidates = candidates.map { if (it.agentId == taken) it.copy(busy = true) else it }
        }
        return woken
    }

    /** The verifier that took [job], or null. */
    private fun handle(
        session: Session,
        tenantId: UUID,
        job: QueuedJob,
        candidates: List<Candidate>,
        now: Instant,
    ): UUID? {
        if (job.sourceDeleted) {
            close(session, tenantId, job.id, now)
            return null
        }
        val space = settings.space.of(job.totalBytes)
        val needs = JobNeeds(job.snapshot.repositoryId, job.plugin, job.sourceMachines, space)
        return when (val match = VerifierChoice.of(needs, candidates)) {
            is Match.Chosen -> start(session, tenantId, job, match, now)
            is Match.NoneFits -> waitOrExpire(session, tenantId, job, match.misfits, now)
        }
    }

    private fun start(
        session: Session,
        tenantId: UUID,
        job: QueuedJob,
        match: Match.Chosen,
        now: Instant,
    ): UUID {
        val verifier = match.candidate.agentId
        val task =
            VerifyTask(
                job.id,
                job.snapshot.repositoryId,
                job.fingerprint,
                job.fingerprintSha256,
                job.deadline,
                settings.stepTimeout.seconds,
            )
        val attempt =
            VerificationAttempt(
                job.snapshot.sourceId,
                verifier,
                job.plugin,
                match.repositoryName,
                job.config,
                job.resticSnapshot,
                task,
            )
        val runId = runs.start(session, attempt, now)
        session
            .createNativeMutationQuery(RUNNING)
            .setParameter("run", runId)
            .setParameter("verifier", verifier)
            .setParameter("tenant", tenantId)
            .setParameter("id", job.id)
            .executeUpdate()
        return verifier
    }

    private fun waitOrExpire(
        session: Session,
        tenantId: UUID,
        job: QueuedJob,
        misfits: List<Misfit>,
        now: Instant,
    ): UUID? {
        if (now < job.deadline) {
            session
                .createNativeMutationQuery(WAITING)
                .setParameter("unsuitable", VerificationHistory.misfitsJson(misfits))
                .setParameter("tenant", tenantId)
                .setParameter("id", job.id)
                .executeUpdate()
            return null
        }
        val reason = if (misfits.isEmpty()) NO_VERIFIER_AT_ALL else "no verifier fits"
        val entry =
            VerificationEntry(job.id, job.snapshot, VerificationStatus.NO_VERIFIER, 0, reason, misfits = misfits)
        history.record(session, tenantId, entry, now)
        close(session, tenantId, job.id, now)
        return null
    }

    private fun queued(
        session: Session,
        tenantId: UUID,
    ): List<QueuedJob> =
        session
            .createNativeQuery(QUEUED.trimIndent(), Array<Any?>::class.java)
            .setParameter("tenant", tenantId)
            .list()
            .map(::QueuedJob)

    private fun close(
        session: Session,
        tenantId: UUID,
        jobId: UUID,
        now: Instant,
    ) = session
        .createNativeMutationQuery(CLOSE)
        .setParameter("now", now)
        .setParameter("tenant", tenantId)
        .setParameter("id", jobId)
        .executeUpdate()
}
