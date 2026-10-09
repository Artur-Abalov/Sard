// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import jakarta.persistence.Column
import jakarta.persistence.Entity
import jakarta.persistence.Id
import jakarta.persistence.Table
import org.hibernate.annotations.JdbcTypeCode
import org.hibernate.annotations.TenantId
import org.hibernate.type.SqlTypes
import java.time.Instant
import java.util.UUID

/** What to back up (migration V202609301200); deleted softly, its runs stay (ADR 0013, rule 3). */
@Entity
@Table(name = "sources")
class SourceRecord(
    @Id
    val id: UUID,
    @Column(name = "agent_id", nullable = false)
    var agentId: UUID,
    @Column(nullable = false)
    var name: String,
    @Column(nullable = false)
    var plugin: String,
    /** JSON object text with secret names only (ADR 0008); PostgreSQL stores it as jsonb. Never logged. */
    @JdbcTypeCode(SqlTypes.JSON)
    @Column(nullable = false)
    var config: String,
    @Column(name = "repository_name", nullable = false)
    var repositoryName: String,
    @Column(name = "created_at", nullable = false, updatable = false)
    val createdAt: Instant,
    @Column(name = "updated_at", nullable = false)
    var updatedAt: Instant,
    @TenantId
    @Column(name = "tenant_id", nullable = false, updatable = false)
    val tenantId: UUID? = null,
) {
    @Column(name = "deleted_at")
    var deletedAt: Instant? = null

    /** Set only for the server's own sources (F6, migration V202610101200); never changes once set. */
    @Column(name = "system_role", updatable = false)
    var systemRole: String? = null

    override fun toString() = "SourceRecord(id=$id, agentId=$agentId, plugin=$plugin)"
}

/**
 * A run of a source (ADR 0022); workflow_id and definition stay NULL until workflows exist; schedule_id
 * names the schedule that started it (F3a). Never deleted.
 */
@Entity
@Table(name = "runs")
class RunRecord(
    @Id
    val id: UUID,
    @Column(name = "source_id", nullable = false, updatable = false)
    val sourceId: UUID,
    @Column(nullable = false, updatable = false)
    val trigger: String,
    @Column(nullable = false)
    val status: String,
    @Column(name = "queued_at", nullable = false, updatable = false)
    val queuedAt: Instant,
    @Column(name = "schedule_id", updatable = false)
    val scheduleId: UUID? = null,
    @TenantId
    @Column(name = "tenant_id", nullable = false, updatable = false)
    val tenantId: UUID? = null,
) {
    @Column
    val message: String? = null

    @Column(name = "started_at")
    val startedAt: Instant? = null

    @Column(name = "finished_at")
    val finishedAt: Instant? = null
}

/**
 * One command to an agent; [id] is RunStep.command_id. Status changes only through guarded
 * updates (ADR 0038), never through this entity, so its mutable columns are read-only here.
 */
@Entity
@Table(name = "run_steps")
class RunStepRecord(
    @Id
    val id: UUID,
    @Column(name = "run_id", nullable = false, updatable = false)
    val runId: UUID,
    @Column(nullable = false, updatable = false)
    val ordinal: Int,
    @Column(name = "agent_id", nullable = false, updatable = false)
    val agentId: UUID,
    @Column(name = "source_id", updatable = false)
    val sourceId: UUID?,
    @Column(nullable = false, updatable = false)
    val plugin: String,
    @Column(nullable = false, updatable = false)
    val action: String,
    @Column(name = "repository_name", updatable = false)
    val repositoryName: String?,
    /** RunStep.config_json as sent. Never logged. */
    @JdbcTypeCode(SqlTypes.JSON)
    @Column(nullable = false, updatable = false)
    val config: String,
    @Column(nullable = false, updatable = false)
    val status: String,
    @Column(name = "queued_at", nullable = false, updatable = false)
    val queuedAt: Instant,
    @TenantId
    @Column(name = "tenant_id", nullable = false, updatable = false)
    val tenantId: UUID? = null,
) {
    @Column(name = "snapshot_id", insertable = false, updatable = false)
    val snapshotId: String? = null

    @Column(insertable = false, updatable = false)
    val phase: String? = null

    @Column(name = "bytes_processed", insertable = false, updatable = false)
    val bytesProcessed: Long? = null

    @Column(name = "bytes_total", insertable = false, updatable = false)
    val bytesTotal: Long? = null

    @Column(insertable = false, updatable = false)
    val message: String? = null

    @Column(name = "files_processed", insertable = false, updatable = false)
    val filesProcessed: Long? = null

    @Column(name = "files_total", insertable = false, updatable = false)
    val filesTotal: Long? = null

    /** What the agent's StepResult produced, as S7a stored it: JSON text. Never logged. */
    @JdbcTypeCode(SqlTypes.JSON)
    @Column(insertable = false, updatable = false)
    val output: String? = null

    @Column(name = "dispatched_at", insertable = false, updatable = false)
    val dispatchedAt: Instant? = null

    @Column(name = "started_at", insertable = false, updatable = false)
    val startedAt: Instant? = null

    @Column(name = "finished_at", insertable = false, updatable = false)
    val finishedAt: Instant? = null

    override fun toString() = "RunStepRecord(id=$id, runId=$runId, status=$status)"
}
