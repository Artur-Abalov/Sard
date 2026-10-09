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

/** A source's schedule (migration V202610091200, F3a); next_run_at is NULL while disabled. */
@Entity
@Table(name = "schedules")
class ScheduleRecord(
    @Id
    val id: UUID,
    @Column(name = "source_id", nullable = false, updatable = false)
    val sourceId: UUID,
    @Column(nullable = false)
    var cron: String,
    @Column(nullable = false)
    var timezone: String,
    @Column(nullable = false)
    var enabled: Boolean,
    @Column(name = "next_run_at")
    var nextRunAt: Instant?,
    @Column(name = "created_at", nullable = false, updatable = false)
    val createdAt: Instant,
    @Column(name = "updated_at", nullable = false)
    var updatedAt: Instant,
    @TenantId
    @Column(name = "tenant_id", nullable = false, updatable = false)
    val tenantId: UUID? = null,
) {
    @Column(name = "catch_up_at")
    var catchUpAt: Instant? = null

    @Column(name = "last_fired_at")
    var lastFiredAt: Instant? = null

    @Column(name = "skipped_in_row", nullable = false)
    var skippedInRow: Int = 0

    override fun toString() = "ScheduleRecord(id=$id, sourceId=$sourceId, cron=$cron, timezone=$timezone)"
}

/** One fire of a schedule and what came of it; written once, never changed or deleted. */
@Entity
@Table(name = "schedule_fires")
class ScheduleFireRecord(
    @Id
    val id: UUID,
    @Column(name = "schedule_id", nullable = false, updatable = false)
    val scheduleId: UUID,
    @Column(nullable = false, updatable = false)
    val kind: String,
    @Column(name = "scheduled_for", nullable = false, updatable = false)
    val scheduledFor: Instant,
    @Column(nullable = false, updatable = false)
    val outcome: String,
    @Column(name = "run_id", updatable = false)
    val runId: UUID?,
    @Column(updatable = false)
    val reason: String?,
    @Column(name = "missed_count", updatable = false)
    val missedCount: Int?,
    @Column(name = "missed_until", updatable = false)
    val missedUntil: Instant?,
    @Column(name = "skipped_in_row", nullable = false, updatable = false)
    val skippedInRow: Int,
    @Column(nullable = false, updatable = false)
    val alert: Boolean,
    @Column(name = "recorded_at", nullable = false, updatable = false)
    val recordedAt: Instant,
    @TenantId
    @Column(name = "tenant_id", nullable = false, updatable = false)
    val tenantId: UUID? = null,
)
