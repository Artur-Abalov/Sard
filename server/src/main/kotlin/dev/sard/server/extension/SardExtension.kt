// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

/**
 * Extension point for enterprise modules (SSO, RBAC, audit, tenants, reports, HA,
 * licensing). An extension is a Spring Boot starter that registers a bean of this
 * type from its own auto-configuration (META-INF/spring/...AutoConfiguration.imports).
 * The open core must start and work with none.
 */
interface SardExtension {
    /** Stable identifier, e.g. "sso". */
    val id: String

    /** Human-readable name shown in the UI. */
    val displayName: String
}
