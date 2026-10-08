// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentCertificateRecord
import dev.sard.server.persistence.AgentPluginRecord
import dev.sard.server.persistence.AgentRepositoryRecord
import dev.sard.server.persistence.PageKey
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.hqlWhere
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

/** The name of the built-in provider (agent `crypto.ResticAESName`). */
private const val BUILT_IN_PROVIDER = "restic-aes"
private val NOBODY = setOf(UUID(0, 0))

/** The HQL twin of [liveBuiltin]: change one, change the other. */
private const val LIVE_BUILTIN_AGENTS = "select count(a) from Agent a where a.builtin = true and a.revokedAt is null"
private const val MARK_DUPLICATE = "update Agent set duplicateSessionAt = :now where id = :agent"

/** The rule "a built-in agent that is not revoked"; [LIVE_BUILTIN_AGENTS] states it in HQL. */
private fun Agent.liveBuiltin(): Boolean = builtin && revokedAt == null

/** Which agents are connected, and the one thing the server does to a connection: refuse a revoked agent's. */
interface AgentPresence {
    fun online(agentId: UUID): Boolean

    fun onlineIds(): Set<UUID>

    /** Ends the agent's open stream because it was revoked; nothing if it has none. */
    fun disconnectRevoked(agentId: UUID)
}

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
    /** The agent next to the server, enrolled with a built-in token (docs/specs/server/self-agent.feature). */
    val builtin: Boolean,
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
    /** The provider that hands the key to restic; restic's own AES when the repository names none (ADR 0008). */
    val cryptoProvider: String,
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

/** The outcome of [Agents.revoke]. */
sealed interface AgentRevocation {
    /** Revoked now, or earlier: revoking a revoked agent changes nothing. */
    data class Revoked(
        val card: AgentCard,
    ) : AgentRevocation

    data object NotFound : AgentRevocation

    /** A live built-in agent is revoked only with [SELF_AGENT_CONFIRMATION]; nothing was changed. */
    data object ConfirmationRequired : AgentRevocation
}

/** What an administrator must pass as `confirm` to revoke a live built-in agent; compared exactly. */
const val SELF_AGENT_CONFIRMATION = "sard-self"

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
    private val presence: AgentPresence,
    private val announcer: RunAnnouncer,
    private val clock: Clock,
) {
    /** Online: the stream manager has a live session of a not revoked agent. */
    fun online(row: AgentRow): Boolean = row.revokedAt == null && presence.online(row.id)

    /** Agents newest first by registration, [after] the key given, in [connectivity] if given, at most [limit]. */
    fun list(
        tenantId: UUID,
        connectivity: Connectivity?,
        after: PageKey?,
        limit: Int,
    ): List<AgentRow> =
        sessions.inTenant(tenantId) { session ->
            val conditions =
                listOfNotNull(after?.let { PageKey.condition("registeredAt") }, connectivityCondition(connectivity))
            val hql = "from Agent ${hqlWhere(conditions)} order by registeredAt desc, id desc"
            val query = session.createSelectionQuery(hql, Agent::class.java)
            // An empty list is no valid IN list: a placeholder id that no agent has stands for "nobody".
            if (connectivity != null) query.setParameterList("online", presence.onlineIds().ifEmpty { NOBODY })
            after?.bind(query)
            query.setMaxResults(limit).list().map { rowOf(it) }
        }

    private fun connectivityCondition(connectivity: Connectivity?): String? =
        when (connectivity) {
            null -> null
            Connectivity.ONLINE -> "id in :online"
            Connectivity.OFFLINE -> "id not in :online"
        }

    /** Whether the tenant has a built-in agent that is not revoked (the rule [revoke] asks confirmation for). */
    fun builtinLive(tenantId: UUID): Boolean =
        sessions.inTenant(tenantId) { session ->
            session.createSelectionQuery(LIVE_BUILTIN_AGENTS, java.lang.Long::class.java).singleResult.toLong() > 0
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
     * a revoked agent changes nothing. A live built-in agent is revoked only when [confirmation] is
     * [SELF_AGENT_CONFIRMATION]; without it nothing changes.
     */
    fun revoke(
        tenantId: UUID,
        agentId: UUID,
        confirmation: String?,
    ): AgentRevocation =
        when (val attempt = sessions.inTenant(tenantId) { attemptIn(it, tenantId, agentId, confirmation) }) {
            Attempt.Missing -> {
                AgentRevocation.NotFound
            }

            Attempt.Unconfirmed -> {
                AgentRevocation.ConfirmationRequired
            }

            is Attempt.Done -> {
                presence.disconnectRevoked(agentId)
                attempt.lostRuns.forEach { announcer.announce(tenantId, it) }
                get(tenantId, agentId)?.let { AgentRevocation.Revoked(it) } ?: AgentRevocation.NotFound
            }
        }

    private fun attemptIn(
        session: Session,
        tenantId: UUID,
        agentId: UUID,
        confirmation: String?,
    ): Attempt {
        // The lock orders this against a run being started for the agent (Runs.start shares it).
        val agent = session.find(Agent::class.java, agentId, LockModeType.PESSIMISTIC_WRITE) ?: return Attempt.Missing
        val unconfirmed = agent.liveBuiltin() && confirmation != SELF_AGENT_CONFIRMATION
        return if (unconfirmed) Attempt.Unconfirmed else Attempt.Done(revokeIn(session, tenantId, agent))
    }

    private sealed interface Attempt {
        data object Missing : Attempt

        data object Unconfirmed : Attempt

        data class Done(
            val lostRuns: List<UUID>,
        ) : Attempt
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
            agent.builtin,
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
            repositories.map {
                AgentRepositoryView(
                    it.name,
                    it.backend,
                    it.repositoryId,
                    it.cryptoProvider ?: BUILT_IN_PROVIDER,
                )
            },
            agent.secretNames,
            agent.scriptNames,
        )
    }
}
