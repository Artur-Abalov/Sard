// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentRepositoryRecord
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.registration.RecoveryKey
import dev.sard.server.role.AgentRole
import java.util.UUID

/** Every report of a repository with an id by an agent that is not revoked; the tenant filter is the session's. */
private const val HOLDERS =
    "select r, a from AgentRepositoryRecord r, Agent a " +
        "where a.id = r.agentId and a.revokedAt is null and r.repositoryId is not null and r.repositoryId <> ''"

private const val REPOSITORIES_OF = "from AgentRepositoryRecord where agentId in :agents order by name"

/** The repositories of a tenant as the agents' last Registers describe them, and the count without a recovery key. */
data class RepositoryOverview(
    val items: List<RepositorySummary>,
    val withoutRecoveryKey: Int,
)

/**
 * What the tenant's agents say about repositories, by repository id (V3, КС5-КС7). [selfCommand] builds the command
 * that issues a recovery key for a repository of the built-in agent, which the self-backup owns.
 */
class Repositories(
    private val sessions: TenantSessions,
    private val selfCommand: (String) -> String,
) {
    fun overview(tenantId: UUID): RepositoryOverview {
        val items = RecoveryKeyRules.summaries(holders(tenantId), selfCommand)
        return RepositoryOverview(items, RecoveryKeyRules.withoutRecoveryKey(items))
    }

    /** What [verifier] still has to connect, by repository id; the card of a verifier shows it (КС7). */
    fun verifierSetup(
        tenantId: UUID,
        verifier: UUID,
    ): List<VerifierSetupItem> = VerifierSetupRules.verifierSetup(holders(tenantId), verifier)

    /** The repositories the given agents announced, by agent, each by name; an agent without any has no entry. */
    fun announcedBy(
        tenantId: UUID,
        agentIds: Collection<UUID>,
    ): Map<UUID, List<AgentRepositoryView>> =
        if (agentIds.isEmpty()) {
            emptyMap()
        } else {
            sessions.inTenant(tenantId) { session ->
                session
                    .createSelectionQuery(REPOSITORIES_OF, AgentRepositoryRecord::class.java)
                    .setParameterList("agents", agentIds)
                    .list()
                    .groupBy({ it.agentId }, { it.toView() })
            }
        }

    private fun holders(tenantId: UUID): List<RepositoryHolder> =
        sessions.inTenant(tenantId) { session ->
            session
                .createSelectionQuery(HOLDERS, Array<Any>::class.java)
                .list()
                .map { row -> holderOf(row[0] as AgentRepositoryRecord, row[1] as Agent) }
        }

    private fun holderOf(
        record: AgentRepositoryRecord,
        agent: Agent,
    ) = RepositoryHolder(
        agent.id,
        agent.hostname,
        AgentRole.of(agent.role),
        agent.builtin,
        checkNotNull(record.repositoryId),
        record.name,
        checkNotNull(agent.lastRegisterAt) { "an agent with a repository has registered" },
        RecoveryKey.of(record.recoveryKey),
    )
}
