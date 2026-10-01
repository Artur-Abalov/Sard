// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.PageKey
import dev.sard.server.persistence.SnapshotRecord
import dev.sard.server.persistence.SourceRecord
import dev.sard.server.persistence.TenantSessions
import java.time.Instant
import java.util.UUID

private const val OF_SOURCE =
    "select s, st.runId from SnapshotRecord s, RunStepRecord st where st.id = s.stepId and s.sourceId = :source"

/** A restic snapshot a step of a source made; [partial] when the step failed after saving it. */
data class SnapshotView(
    val id: UUID,
    val snapshotId: String,
    val sourceId: UUID,
    val runId: UUID,
    val stepId: UUID,
    val agentId: UUID,
    val repositoryName: String,
    val repositoryId: String,
    val totalBytes: Long,
    val addedBytes: Long,
    val createdAt: Instant,
    val forgottenAt: Instant?,
    val partial: Boolean,
)

/** The snapshots of a source (S8b): its history stays when the source is deleted (ADR 0013, rule 3). */
class Snapshots(
    private val sessions: TenantSessions,
) {
    /**
     * The source's snapshots newest first, after [after] if given, at most [limit]; null when the tenant has
     * no such source (deleted or not).
     */
    fun ofSource(
        tenantId: UUID,
        sourceId: UUID,
        after: PageKey?,
        limit: Int,
    ): List<SnapshotView>? =
        sessions.inTenant(tenantId) { session ->
            session.find(SourceRecord::class.java, sourceId) ?: return@inTenant null
            val paging = after?.let { " and ${PageKey.condition("s.createdAt", "s.id")}" }.orEmpty()
            val query =
                session
                    .createSelectionQuery("$OF_SOURCE$paging order by s.createdAt desc, s.id desc", Array<Any?>::class.java)
                    .setParameter("source", sourceId)
            after?.bind(query)
            query.setMaxResults(limit).list().map { viewOf(it[0] as SnapshotRecord, it[1] as UUID) }
        }

    private fun viewOf(
        record: SnapshotRecord,
        runId: UUID,
    ) = SnapshotView(
        record.id,
        record.snapshotId,
        record.sourceId,
        runId,
        record.stepId,
        record.agentId,
        record.repositoryName,
        record.repositoryId,
        record.totalBytes,
        record.addedBytes,
        record.createdAt,
        record.forgottenAt,
        record.partial,
    )
}
