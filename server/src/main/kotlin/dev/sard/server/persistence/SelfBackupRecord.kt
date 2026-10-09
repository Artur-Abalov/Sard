// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import jakarta.persistence.Column
import jakarta.persistence.Entity
import jakarta.persistence.Id
import jakarta.persistence.Table
import java.time.Instant
import java.util.UUID

/** The self-backup's binding of a tenant (F6, migration V202610101200); its sources carry agent and repository. */
@Entity
@Table(name = "self_backups")
class SelfBackupRecord(
    @Id
    @Column(name = "tenant_id")
    val tenantId: UUID,
    @Column(name = "local_storage_confirmed", nullable = false)
    var localStorageConfirmed: Boolean,
    @Column(name = "bound_at", nullable = false)
    var boundAt: Instant,
    @Column(name = "updated_at", nullable = false)
    var updatedAt: Instant,
) {
    override fun toString() = "SelfBackupRecord(tenantId=$tenantId, boundAt=$boundAt)"
}
