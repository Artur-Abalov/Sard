// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.persistence.TenantSessions
import org.hibernate.Session

/**
 * Database access for AgentService handlers: always in the tenant of the certificate the
 * call was authenticated with (ADR 0013), never one named in a message. The principal
 * lives in the call's gRPC Context, which grpc-kotlin carries into the handler coroutine
 * and every dispatcher it switches to, for the whole call or stream.
 */
class AgentSessions(
    private val sessions: TenantSessions,
) {
    /** Fails outside an authenticated agent call rather than guess a tenant. */
    fun <T> inTenant(work: (Session) -> T): T {
        val principal = checkNotNull(AgentPrincipal.KEY.get()) { "not inside an authenticated agent call" }
        return sessions.inTenant(principal.tenantId, work)
    }
}
