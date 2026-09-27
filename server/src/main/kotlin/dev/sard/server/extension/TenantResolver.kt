// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import java.util.UUID

/**
 * Names the tenant whose data the current caller may see (ADR 0013). The open core
 * pins it to [DEFAULT_TENANT_ID]; an enterprise starter replaces the bean and must
 * throw when no tenant is known rather than fall back to a default.
 */
fun interface TenantResolver {
    fun currentTenantId(): UUID

    companion object {
        /** The only tenant of the open core, seeded by Flyway V2. Never change it. */
        val DEFAULT_TENANT_ID: UUID = UUID.fromString("00000000-0000-0000-0000-000000000001")
    }
}
