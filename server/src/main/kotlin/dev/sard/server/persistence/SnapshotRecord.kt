// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import jakarta.persistence.Column
import jakarta.persistence.Entity
import jakarta.persistence.Id
import jakarta.persistence.Table
import org.hibernate.annotations.TenantId
import java.time.Instant
import java.util.UUID

/** A restic snapshot made by a step (migration V202609301800): written by S7a's native insert, read here. */
@Entity
@Table(name = "snapshots")
class SnapshotRecord(
    @Id
    val id: UUID,
    @Column(name = "snapshot_id", nullable = false, insertable = false, updatable = false)
    val snapshotId: String,
    @Column(name = "source_id", nullable = false, insertable = false, updatable = false)
    val sourceId: UUID,
    @Column(name = "step_id", nullable = false, insertable = false, updatable = false)
    val stepId: UUID,
    @Column(name = "agent_id", nullable = false, insertable = false, updatable = false)
    val agentId: UUID,
    @Column(name = "repository_name", nullable = false, insertable = false, updatable = false)
    val repositoryName: String,
    @Column(name = "repository_id", nullable = false, insertable = false, updatable = false)
    val repositoryId: String,
    @Column(name = "total_bytes", nullable = false, insertable = false, updatable = false)
    val totalBytes: Long,
    @Column(name = "added_bytes", nullable = false, insertable = false, updatable = false)
    val addedBytes: Long,
    @Column(name = "created_at", nullable = false, insertable = false, updatable = false)
    val createdAt: Instant,
    @Column(name = "forgotten_at", insertable = false, updatable = false)
    val forgottenAt: Instant?,
    /** The step failed after saving it (S8b В8). */
    @Column(nullable = false, insertable = false, updatable = false)
    val partial: Boolean,
    @TenantId
    @Column(name = "tenant_id", nullable = false, insertable = false, updatable = false)
    val tenantId: UUID? = null,
)
