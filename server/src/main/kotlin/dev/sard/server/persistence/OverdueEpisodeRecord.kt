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

/**
 * A time a scheduled source went without a success for too long (F6, migration V202610121200). Rows are written
 * and closed only by guarded native statements in `freshness/FreshnessCheck`; the entity serves reads.
 */
@Entity
@Table(name = "overdue_episodes")
class OverdueEpisodeRecord(
    @Id
    val id: UUID,
    @Column(name = "source_id", nullable = false, updatable = false)
    val sourceId: UUID,
    /** The success the episode counts from; null when the source never succeeded. */
    @Column(name = "counted_from", updatable = false)
    val countedFrom: Instant?,
    @Column(nullable = false, updatable = false)
    val deadline: Instant,
    @Column(name = "opened_at", nullable = false, updatable = false)
    val openedAt: Instant,
    @Column(name = "closed_at", updatable = false)
    val closedAt: Instant?,
    /** The success that ended it; null while open, or when the source lost its schedule or was deleted. */
    @Column(name = "closed_by_run_id", updatable = false)
    val closedByRunId: UUID?,
    @TenantId
    @Column(name = "tenant_id", nullable = false, updatable = false)
    val tenantId: UUID? = null,
) {
    override fun toString() = "OverdueEpisodeRecord(id=$id, sourceId=$sourceId, deadline=$deadline)"
}
