// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentOwnedKey
import dev.sard.server.persistence.AgentPluginRecord
import dev.sard.server.persistence.AgentRepositoryRecord
import org.hibernate.Session
import java.util.UUID

/**
 * What the agent's last Register offers (S4a snapshot, ADR 0022). The snapshot can be stale, so
 * the agent may still answer REJECTED; this only refuses what is already known to fail.
 */
internal object AgentOffer {
    /** Refuses, in this order, an unknown or revoked agent, an unknown plugin, an unknown repository. */
    fun require(
        session: Session,
        agentId: UUID,
        plugin: String,
        repositoryName: String,
    ) {
        val agent = session.find(Agent::class.java, agentId)
        val refusal =
            when {
                agent == null || agent.revokedAt != null -> {
                    UnknownAgent(agentId)
                }

                session.find(AgentPluginRecord::class.java, AgentOwnedKey(agentId, plugin)) == null -> {
                    UnknownPlugin(plugin)
                }

                session.find(AgentRepositoryRecord::class.java, AgentOwnedKey(agentId, repositoryName)) == null -> {
                    UnknownRepository(repositoryName)
                }

                else -> {
                    null
                }
            }
        refusal?.let { throw it }
    }
}
