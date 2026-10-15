// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.role.AgentRole as DomainRole

/** The wire role as the domain knows it. Exhaustive, no `else`. */
internal fun AgentRole.toDomain(): DomainRole =
    when (this) {
        AgentRole.SOURCE -> DomainRole.SOURCE
        AgentRole.VERIFIER -> DomainRole.VERIFIER
    }

/** The domain role as the wire knows it. Exhaustive, no `else`. */
internal fun DomainRole.toApi(): AgentRole =
    when (this) {
        DomainRole.SOURCE -> AgentRole.SOURCE
        DomainRole.VERIFIER -> AgentRole.VERIFIER
    }
