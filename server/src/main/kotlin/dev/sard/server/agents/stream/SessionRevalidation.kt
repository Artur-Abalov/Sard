// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import dev.sard.server.agents.AgentAuthFailure
import dev.sard.server.agents.AgentAuthResult
import dev.sard.server.agents.AgentAuthStatus
import dev.sard.server.agents.AgentAuthenticator
import dev.sard.server.agents.BatchStandings
import dev.sard.server.agents.CertificateStanding
import dev.sard.server.agents.CertificateStandings
import dev.sard.server.agents.PresentedCertificate
import dev.sard.server.pki.AgentIdentity
import java.time.Clock

/** Closes a stream as the interceptor would have refused it (S3's reasons, UNAUTHENTICATED). */
fun AgentAuthFailure.close(): StreamClose = StreamClose(name) { AgentAuthStatus.of(this) }

/**
 * Re-judges open sessions against the database with S3's [AgentAuthenticator], so a revoked
 * agent or certificate, or one past `not_after`, fails here exactly as it would on a new call.
 */
class SessionRevalidation(
    private val standings: BatchStandings,
    private val clock: Clock,
) {
    fun failures(agents: List<ConnectedAgent>): Map<ConnectedAgent, AgentAuthFailure> {
        if (agents.isEmpty()) return emptyMap()
        val known = standings.of(agents.map { it.serial })
        val authenticator = AgentAuthenticator(CertificateStandings(known::get), clock)
        return agents
            .mapNotNull { agent ->
                (authenticator.authenticate(presented(agent)) as? AgentAuthResult.Rejected)?.let { agent to it.failure }
            }.toMap()
    }

    /** What the certificate presented when the stream opened; its SAN matched the record then. */
    private fun presented(agent: ConnectedAgent) =
        PresentedCertificate(agent.serial, AgentIdentity(tenantId = agent.tenantId, agentId = agent.agentId))
}
