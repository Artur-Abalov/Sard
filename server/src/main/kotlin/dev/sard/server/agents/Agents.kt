// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.agents.stream.AgentConnections
import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentCertificateRecord
import dev.sard.server.persistence.AgentPluginRecord
import dev.sard.server.persistence.AgentRepositoryRecord
import dev.sard.server.persistence.PageKey
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.runs.RunAnnouncer
import dev.sard.server.runs.StepRevocation
import jakarta.persistence.LockModeType
import org.hibernate.Session
import java.time.Clock
import java.time.Instant
import java.util.UUID

private const val BY_AGENT = "from AgentPluginRecord where agentId = :agent order by name"
private const val REPOSITORIES = "from AgentRepositoryRecord where agentId = :agent order by name"
private const val REVOKE_CERTIFICATES =
    "update AgentCertificateRecord set revokedAt = :now where agentId = :agent and revokedAt is null"
private const val MARK_DUPLICATE = "update Agent set duplicateSessionAt = :now where id = :agent"

/** What an agent is, without what it announced in Register: a row of the list. */
data class AgentRow(
    val id: UUID,
    val hostname: String,
    val agentVersion: String?,
    val os: String?,
    val arch: String?,
    val registeredAt: Instant,
    val lastSeenAt: Instant?,
    val revokedAt: Instant?,
    val duplicateSessionAt: Instant?,
)

/** A plugin the agent announced; [configSchema] is the JSON text it sent. */
data class AgentPluginView(
    val name: String,
    val version: String,
    val actions: List<String>,
    val configSchema: String,
)

/** A repository the agent announced: names and ids, never a key (ADR 0008). */
data class AgentRepositoryView(
    val name: String,
    val backend: String,
    val repositoryId: String?,
    val cryptoProvider: String?,
)

/** An agent with what its last Register announced. */
data class AgentCard(
    val row: AgentRow,
    val protocolVersion: Int?,
    val plugins: List<AgentPluginView>,
    val repositories: List<AgentRepositoryView>,
    val secretNames: List<String>,
    val scriptNames: List<String>,
)

/** Which agents a list shows by their connection; null of it shows all. */
enum class Connectivity { ONLINE, OFFLINE }

/**
 * The agents of a tenant as an administrator sees them (S8b): the data of the last Register, whether the
 * stream manager holds a session ([online], ADR 0026: no threshold on `last_seen_at`), and revocation.
 * Revoking is one transaction that also ends the agent's certificates and its waiting and running steps,
 * and only after it commits the open stream is closed: a failed revocation closes nothing.
 */
class Agents(
    private val sessions: TenantSessions,
    private val connections: AgentConnections,
    private val announcer: RunAnnouncer,
    private val clock: Clock,
) {
    /** Online: the stream manager has a live session of a not revoked agent. */
    fun online(row: AgentRow): Boolean = row.revokedAt == null && connections.online(row.id)

    /** Agents newest first by registration, [after] the key given, in [connectivity] if given, at most [limit]. */
    fun list(
        tenantId: UUID,
        connectivity: Connectivity?,
        after: PageKey?,
        limit: Int,
    ): List<AgentRow> =
        sessions.inTenant(tenantId) { session ->
            val online = connections.onlineIds()
            val conditions =
                listOfNotNull(
                    after?.let { PageKey.condition("registeredAt") },
                    connectivityCondition(connectivity, online),
                )
            val where = conditions.takeIf { it.isNotEmpty() }?.joinToString(" and ", "where ").orEmpty()
            val query =
                session.createSelectionQuery(
                    "from Agent $where order by registeredAt desc, id desc",
                    Agent::class.java,
                )
            after?.bind(query)
            if (":online" in where) query.setParameterList("online", online)
            query.setMaxResults(limit).list().map { rowOf(it) }
        }

    /** An empty set matches nothing for ONLINE and everything for OFFLINE, so no condition is needed there. */
    private fun connectivityCondition(
        connectivity: Connectivity?,
        online: Set<UUID>,
    ): String? =
        when {
            connectivity == null -> null
            connectivity == Connectivity.ONLINE -> "id in :online".takeIf { online.isNotEmpty() } ?: "1 = 0"
            else -> "id not in :online".takeIf { online.isNotEmpty() }
        }

    fun get(
        tenantId: UUID,
        agentId: UUID,
    ): AgentCard? =
        sessions.inTenant(tenantId) { session ->
            session.find(Agent::class.java, agentId)?.let {
                cardOf(session, it)
            }
        }

    /**
     * Revokes [agentId]: its certificates and the agent itself, its queued, dispatched and running steps
     * become lost (their runs failed, S8b В5), then its open stream is closed as AGENT_REVOKED. Revoking
     * a revoked agent changes nothing. Null when the tenant has no such agent.
     */
    fun revoke(
        tenantId: UUID,
        agentId: UUID,
    ): AgentCard? {
        val revoked =
            sessions.inTenant(tenantId) { session ->
                // The lock orders this against a run being started for the agent (Runs.start shares it).
                val agent = session.find(Agent::class.java, agentId, LockModeType.PESSIMISTIC_WRITE)
                agent?.let { it to revokeIn(session, tenantId, it) }
            } ?: return null
        connections.close(agentId, AgentAuthFailure.AGENT_REVOKED)
        revoked.second.forEach { announcer.announce(tenantId, it) }
        return get(tenantId, agentId)
    }

    private fun revokeIn(
        session: Session,
        tenantId: UUID,
        agent: Agent,
    ): List<UUID> {
        if (agent.revokedAt != null) return emptyList()
        val now = clock.instant()
        agent.revokedAt = now
        session
            .createMutationQuery(REVOKE_CERTIFICATES)
            .setParameter("now", now)
            .setParameter("agent", agent.id)
            .executeUpdate()
        return StepRevocation.loseSteps(session, tenantId, agent.id, now)
    }

    /** Records a confirmed duplicate session of [agentId] at the clock's time (ADR 0026, rule 2). */
    fun markDuplicate(
        tenantId: UUID,
        agentId: UUID,
    ) {
        sessions.inTenant(tenantId) { session ->
            session
                .createMutationQuery(MARK_DUPLICATE)
                .setParameter("now", clock.instant())
                .setParameter("agent", agentId)
                .executeUpdate()
        }
    }

    private fun rowOf(agent: Agent) =
        AgentRow(
            agent.id,
            agent.hostname,
            agent.agentVersion,
            agent.os,
            agent.arch,
            agent.registeredAt,
            agent.lastSeenAt,
            agent.revokedAt,
            agent.duplicateSessionAt,
        )

    private fun cardOf(
        session: Session,
        agent: Agent,
    ): AgentCard {
        val plugins =
            session
                .createSelectionQuery(
                    BY_AGENT,
                    AgentPluginRecord::class.java,
                ).setParameter("agent", agent.id)
                .list()
        val repositories =
            session
                .createSelectionQuery(
                    REPOSITORIES,
                    AgentRepositoryRecord::class.java,
                ).setParameter("agent", agent.id)
                .list()
        return AgentCard(
            rowOf(agent),
            agent.protocolVersion,
            plugins.map { AgentPluginView(it.name, it.version, it.actions, it.configSchema) },
            repositories.map { AgentRepositoryView(it.name, it.backend, it.repositoryId, it.cryptoProvider) },
            agent.secretNames,
            agent.scriptNames,
        )
    }
}
