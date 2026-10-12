// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentOwnedKey
import dev.sard.server.persistence.AgentPluginRecord
import dev.sard.server.persistence.AgentRepositoryRecord
import dev.sard.server.pki.AgentRole
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
     * Refuses, in this order, an unknown agent, a revoked one, a verifier (it cannot back up), an unknown plugin
     * (with [requireBackup], also one that does not list backup), an unknown repository.
     * [lock] is taken on the agent's row: a run being started shares it, so it cannot slip past a revocation.
     */
    fun require(
        session: Session,
        agentId: UUID,
        plugin: String,
        repositoryName: String,
        lock: LockModeType = LockModeType.NONE,
        requireBackup: Boolean = false,
    ): Offered {
        val agent = liveAgent(session, agentId, lock)
        requireSource(agent)
        val announced = announcedPlugin(session, agentId, plugin, requireBackup)
        session.find(AgentRepositoryRecord::class.java, AgentOwnedKey(agentId, repositoryName))
            ?: throw UnknownRepository(repositoryName)
        return Offered(announced.configSchema, agent.secretNames.toSet())
    }

    // A source of stage 1 backs up: a plugin that does not list backup is as good as unknown (W2 K17).
    private fun announcedPlugin(
        session: Session,
        agentId: UUID,
        plugin: String,
        requireBackup: Boolean,
    ): AgentPluginRecord {
        val announced = session.find(AgentPluginRecord::class.java, AgentOwnedKey(agentId, plugin))
        if (announced == null || (requireBackup && Action.BACKUP.stored !in announced.actions)) {
            throw UnknownPlugin(plugin)
        }
        return announced
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

    /** A source is backed up by an agent whose role allows backups; a verifier does not (V1). */
    private fun requireSource(agent: Agent) {
        if (!AgentRole.of(agent.role).allows(Action.BACKUP)) throw AgentRoleMismatch(agent.id)
    }
}
