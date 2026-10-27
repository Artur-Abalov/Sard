// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import dev.sard.server.agents.AgentSessions
import dev.sard.server.registration.VerifierReport

// Native SQL names tenant_id explicitly (ADR 0013, rule 8); the columns Register writes (Registration.verifier).
private const val STATUS =
    "update agents set verify_free_bytes = :free, verify_sandbox = :sandbox, verify_sandbox_version = :version, " +
        "verify_images = cast(:images as jsonb) where tenant_id = :tenant and id = :id and verifier_reported"

/**
 * A verifier's status from a heartbeat (V5), in the agent's own tenant: free space changes between Registers. An
 * agent that never reported a status in Register (older than V4) keeps none.
 */
class AgentVerifierStatus(
    private val sessions: AgentSessions,
) : VerifierStatusStore {
    override fun record(
        agent: ConnectedAgent,
        report: VerifierReport,
    ) {
        sessions.inTenant { session ->
            session
                .createNativeMutationQuery(STATUS)
                .setParameter("free", report.workFreeBytes, Long::class.javaObjectType)
                .setParameter("sandbox", report.sandbox?.stored, String::class.java)
                .setParameter("version", report.sandboxVersion, String::class.java)
                .setParameter("images", report.imagesJson())
                .setParameter("tenant", agent.tenantId)
                .setParameter("id", agent.agentId)
                .executeUpdate()
        }
    }
}
