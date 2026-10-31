// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.events.SettingsEvents
import dev.sard.server.runs.SourceNotFound
import org.hibernate.Session
import java.util.UUID

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). The update skips a source that already is as asked, so
// its row count says whether this call changed anything, and it takes the row lock a concurrent call waits for.
private const val ENABLE =
    "update sources set verification_enabled = :enabled where tenant_id = :tenant and id = :id " +
        "and deleted_at is null and verification_enabled is distinct from :enabled"
private const val LIVE = "select count(*) from sources where tenant_id = :tenant and id = :id and deleted_at is null"

/** Turns the restore verification of a source on or off (V4) and tells the listeners when that changed it (Р28). */
class VerificationSwitch(
    private val events: SettingsEvents,
) {
    /** Throws [SourceNotFound] when the tenant has no live source [sourceId]. */
    fun set(
        session: Session,
        tenantId: UUID,
        sourceId: UUID,
        enabled: Boolean,
    ) {
        val changed =
            session
                .createNativeMutationQuery(ENABLE)
                .setParameter("enabled", enabled)
                .setParameter("tenant", tenantId)
                .setParameter("id", sourceId)
                .executeUpdate()
        events.changedRows(changed, session, tenantId, "source_verification", sourceId)
        if (changed == 0 && !live(session, tenantId, sourceId)) throw SourceNotFound(sourceId)
    }

    private fun live(
        session: Session,
        tenantId: UUID,
        sourceId: UUID,
    ): Boolean =
        session
            .createNativeQuery(LIVE, Long::class.javaObjectType)
            .setParameter("tenant", tenantId)
            .setParameter("id", sourceId)
            .singleResult > 0
}
