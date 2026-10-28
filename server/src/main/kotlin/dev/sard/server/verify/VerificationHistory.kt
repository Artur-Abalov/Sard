// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.persistence.UuidV7
import dev.sard.server.runs.StepOutput
import org.hibernate.Session
import tools.jackson.databind.json.JsonMapper
import java.time.Instant
import java.util.UUID

private val JSON = JsonMapper.builder().build()

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). key_holders: live agents of the tenant that report
// the repository's id (ADR 0008). recovered: a pass right after a failure of the same source (V6: «passes again»); a
// cancelled verification proves nothing, so it is looked through (J1f, ВJf2).
private const val RECORD = """
    insert into restore_verifications (id, tenant_id, job_id, source_id, snapshot_id, step_id, verifier_agent_id,
        key_holders, status, level, reason, stages, mismatches, unsuitable, restore_duration_ms, recovered, verified_at,
        cancelled_by, cancelled_at)
    values (:id, :tenant, :job, :source, :snapshot, :step, :verifier,
        (select count(distinct r.agent_id) from agent_repositories r
            join agents a on a.tenant_id = r.tenant_id and a.id = r.agent_id
            where r.tenant_id = :tenant and r.repository_id = :repository and a.revoked_at is null),
        :status, :level, :reason, cast(:stages as jsonb), cast(:mismatches as jsonb), cast(:unsuitable as jsonb),
        :restore,
        :status = 'passed' and coalesce((select last.status = 'failed' from restore_verifications last
            where last.tenant_id = :tenant and last.source_id = :source and last.status <> 'cancelled'
            order by last.verified_at desc, last.id desc limit 1), false),
        :now, :cancelledBy, :cancelledAt)"""

/** Which snapshot of which source a verification is about. */
data class VerifiedSnapshot(
    val sourceId: UUID,
    /** snapshots.id. */
    val snapshotRef: UUID,
    val repositoryId: String,
)

/** One row of restore_verifications: the end of a job. */
data class VerificationEntry(
    val jobId: UUID,
    val snapshot: VerifiedSnapshot,
    val status: VerificationStatus,
    val level: Int,
    val reason: String?,
    val stepId: UUID? = null,
    val verifierId: UUID? = null,
    val output: StepOutput.Verify? = null,
    val misfits: List<Misfit>? = null,
    /** Who cancelled the job and when it was asked; both set for [VerificationStatus.CANCELLED], null otherwise. */
    val cancelledBy: String? = null,
    val cancelledAt: Instant? = null,
)

/** A verifier's evidence as stored: stages and mismatches as JSON arrays, empty without output. */
private class Evidence(
    val stages: String,
    val mismatches: String,
    val restoreMillis: Long?,
) {
    companion object {
        private val NONE = Evidence("[]", "[]", null)

        fun of(output: StepOutput.Verify?): Evidence =
            output?.let {
                Evidence(
                    JSON.writeValueAsString(it.stages.map { stage -> stage.fields() }),
                    JSON.writeValueAsString(it.mismatches.map { mismatch -> mismatch.fields() }),
                    it.restoreMillis,
                )
            } ?: NONE
    }
}

/** Writes the immutable history of verifications (ADR 0013, restore_verifications). */
class VerificationHistory(
    private val ids: UuidV7,
) {
    fun record(
        session: Session,
        tenantId: UUID,
        entry: VerificationEntry,
        now: Instant,
    ) {
        val evidence = Evidence.of(entry.output)
        session
            .createNativeMutationQuery(RECORD.trimIndent())
            .setParameter("id", ids.next())
            .setParameter("tenant", tenantId)
            .setParameter("job", entry.jobId)
            .setParameter("source", entry.snapshot.sourceId)
            .setParameter("snapshot", entry.snapshot.snapshotRef)
            .setParameter("step", entry.stepId, UUID::class.java)
            .setParameter("verifier", entry.verifierId, UUID::class.java)
            .setParameter("repository", entry.snapshot.repositoryId)
            .setParameter("status", entry.status.stored)
            .setParameter("level", entry.level.toShort())
            .setParameter("reason", entry.reason, String::class.java)
            .setParameter("stages", evidence.stages)
            .setParameter("mismatches", evidence.mismatches)
            .setParameter("unsuitable", entry.misfits?.let(::misfitsJson), String::class.java)
            .setParameter("restore", evidence.restoreMillis, Long::class.javaObjectType)
            .setParameter("now", now)
            .setParameter("cancelledBy", entry.cancelledBy, String::class.java)
            .setParameter("cancelledAt", entry.cancelledAt, Instant::class.java)
            .executeUpdate()
    }

    companion object {
        /** Why each verifier did not fit, for REST and V6: `[{agentId, hostname, reasons: [...]}]`. */
        fun misfitsJson(misfits: List<Misfit>): String =
            JSON.writeValueAsString(
                misfits.map { misfit ->
                    mapOf(
                        "agentId" to misfit.agentId.toString(),
                        "hostname" to misfit.hostname,
                        "reasons" to misfit.reasons.map { it.stored }.sorted(),
                    )
                },
            )
    }
}
