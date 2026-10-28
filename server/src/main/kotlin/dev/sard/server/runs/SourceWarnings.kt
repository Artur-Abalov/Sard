// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentOwnedKey
import dev.sard.server.persistence.AgentPluginRecord
import dev.sard.server.persistence.TenantSessions
import org.springframework.stereotype.Component
import java.util.UUID

/**
 * What the agents will refuse of sources by the bindings of their secrets (S11), from their last Register: the
 * console shows it with the source, before a run fails. [Sources] stores and judges a source; this only reads.
 */
@Component
class SourceWarnings(
    private val sessions: TenantSessions,
) {
    /** The warnings of each of [sources] of the tenant, by id; a source without any has an empty list. */
    fun of(
        tenantId: UUID,
        sources: List<SourceView>,
    ): Map<UUID, List<String>> =
        sessions.inTenant(tenantId) { session ->
            sources.associate { source ->
                val agent = session.find(Agent::class.java, source.agentId)
                val plugin = session.find(AgentPluginRecord::class.java, AgentOwnedKey(source.agentId, source.plugin))
                val warnings =
                    if (agent == null || plugin == null) {
                        emptyList()
                    } else {
                        SecretBindings.warnings(source.plugin, plugin.configSchema, source.config, agent.secretBindings)
                    }
                source.id to warnings
            }
        }
}
