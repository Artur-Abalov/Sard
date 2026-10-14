// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import dev.sard.server.persistence.SourceRecord
import dev.sard.server.persistence.TenantSessions
import java.util.UUID

/** Which sources are the self-backup's: the REST guard keeps a system source out of reach (F4b, Р22). */
class SystemSourceLookup(
    private val sessions: TenantSessions,
) {
    /** Whether [sourceId] is a live system source of the tenant; one of another tenant or a deleted one is not. */
    fun isSystemSource(
        tenantId: UUID,
        sourceId: UUID,
    ): Boolean =
        sessions.inTenant(tenantId) { session ->
            val source = session.find(SourceRecord::class.java, sourceId)
            source != null && source.systemRole != null && source.deletedAt == null
        }
}
