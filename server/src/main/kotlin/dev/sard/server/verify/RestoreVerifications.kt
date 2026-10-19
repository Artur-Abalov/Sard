// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.extension.TenantResolver
import org.springframework.stereotype.Component
import java.time.Instant

/** The figure of `GET /status`: when restore was last proven for every source that is verified. */
fun interface RestoreVerifications {
    /** The oldest of the sources' last successful verifications; null if one has none or none is verified (V4). */
    fun lastVerifiedAt(): Instant?
}

/** [RestoreVerifications] read from the history of the current tenant (restore_verifications, V4). */
@Component
class RecordedRestoreVerifications(
    private val verifications: Verifications,
    private val tenants: TenantResolver,
) : RestoreVerifications {
    override fun lastVerifiedAt(): Instant? = verifications.lastVerifiedAt(tenants.currentTenantId())
}
