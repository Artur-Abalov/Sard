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
 * One notification of one run through one channel (migration V202610021200, S9a). Rows are
 * written and moved only by guarded native statements in `notify/Deliveries`, so the entity
 * serves reads and the schema validation.
 */
@Entity
@Table(name = "notification_deliveries")
class NotificationDeliveryRecord(
    @Id
    val id: UUID,
    @Column(name = "run_id", nullable = false, updatable = false)
    val runId: UUID,
    @Column(nullable = false, updatable = false)
    val channel: String,
    @Column(nullable = false, updatable = false)
    val status: String,
    @Column(nullable = false, updatable = false)
    val attempts: Int,
    @Column(name = "next_attempt_at", nullable = false, updatable = false)
    val nextAttemptAt: Instant,
    @Column(name = "created_at", nullable = false, updatable = false)
    val createdAt: Instant,
    @Column(name = "finished_at", updatable = false)
    val finishedAt: Instant?,
    @Column(name = "last_error", updatable = false)
    val lastError: String?,
    @TenantId
    @Column(name = "tenant_id", nullable = false, updatable = false)
    val tenantId: UUID? = null,
) {
    override fun toString() = "NotificationDeliveryRecord(id=$id, runId=$runId, channel=$channel, status=$status)"
}
