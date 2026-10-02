// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentOwnedKey
import dev.sard.server.persistence.AgentPluginRecord
import dev.sard.server.persistence.AgentRepositoryRecord
import jakarta.persistence.LockModeType
import org.hibernate.Session
import java.util.UUID

/** What the agent's last Register offers for one plugin: its config schema and the secrets it holds. */
internal class Offered(
    private val configSchema: String,
    private val secretNames: Set<String>,
) {
    /** Refuses a config that does not fit the plugin's schema or names a secret the agent lacks (S8b В15). */
    fun requireConfig(config: String) {
        val violations = ConfigCheck.violations(configSchema, config, secretNames)
        if (violations.isNotEmpty()) throw InvalidConfig(violations)
    }
}

/**
 * What the agent's last Register offers (S4a snapshot, ADR 0022). The snapshot can be stale, so
 * the agent may still answer REJECTED; this only refuses what is already known to fail.
 */
internal object AgentOffer {
    /**
     * Refuses, in this order, an unknown agent, a revoked one, an unknown plugin, an unknown repository.
     * [lock] is taken on the agent's row: a run being started shares it, so it cannot slip past a revocation.
     */
    fun require(
        session: Session,
        agentId: UUID,
        plugin: String,
        repositoryName: String,
        lock: LockModeType = LockModeType.NONE,
    ): Offered {
        val agent = liveAgent(session, agentId, lock)
        val announced =
            session.find(AgentPluginRecord::class.java, AgentOwnedKey(agentId, plugin)) ?: throw UnknownPlugin(plugin)
        session.find(AgentRepositoryRecord::class.java, AgentOwnedKey(agentId, repositoryName))
            ?: throw UnknownRepository(repositoryName)
        return Offered(announced.configSchema, agent.secretNames.toSet())
    }

    private fun liveAgent(
        session: Session,
        agentId: UUID,
        lock: LockModeType,
    ): Agent {
        val agent = session.find(Agent::class.java, agentId, lock) ?: throw UnknownAgent(agentId)
        if (agent.revokedAt != null) throw AgentRevoked(agentId)
        return agent
    }
}
