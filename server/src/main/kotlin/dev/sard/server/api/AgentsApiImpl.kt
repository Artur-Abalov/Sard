// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.events.SettingsEvents
import dev.sard.server.extension.TenantResolver
import dev.sard.server.fleet.AgentCard
import dev.sard.server.fleet.AgentCertificates
import dev.sard.server.fleet.AgentPluginView
import dev.sard.server.fleet.AgentRepositoryView
import dev.sard.server.fleet.AgentRevocation
import dev.sard.server.fleet.AgentRow
import dev.sard.server.fleet.Agents
import dev.sard.server.fleet.Connectivity
import dev.sard.server.fleet.CurrentCertificate
import dev.sard.server.fleet.Repositories
import dev.sard.server.fleet.VerifierSetupItem
import dev.sard.server.install.AgentVersions
import dev.sard.server.persistence.PageKey
import org.springframework.stereotype.Component
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.time.Clock
import java.util.UUID
import dev.sard.server.role.AgentRole as DomainRole

/** The agent endpoints over [Agents], in the tenant of the session. */
@Component
class AgentsApiImpl(
    private val agents: Agents,
    private val tenants: TenantResolver,
    private val mapper: ObjectMapper,
    private val versions: AgentVersions,
    private val certificates: AgentCertificates,
    private val clock: Clock,
    private val repositories: Repositories,
    private val events: SettingsEvents,
) : AgentsApi {
    override fun listAgents(
        status: AgentStatus?,
        cursor: String?,
        limit: Int,
    ): AgentPage {
        val page = pageRequest(CursorKind.AGENTS, cursor, limit)
        val connectivity = status?.let(::connectivityOf)
        val rows = agents.list(tenants.currentTenantId(), connectivity, page.after, page.fetch)
        val slice = page.slice(rows) { PageKey(it.registeredAt, it.id) }
        val current = certificates.current(tenants.currentTenantId(), slice.items.map { it.id })
        val announced = repositories.announcedBy(tenants.currentTenantId(), slice.items.map { it.id })
        val summaries =
            slice.items.map { row ->
                summaryOf(row, current[row.id], announced[row.id].orEmpty().map { it.toApi() })
            }
        return AgentPage(summaries, slice.nextCursor)
    }

    override fun getAgent(agentId: UUID): AgentDetails =
        detailsOf(agents.get(tenants.currentTenantId(), agentId) ?: throw ResourceNotFound())

    override fun revokeAgent(
        agentId: UUID,
        confirm: String?,
    ): AgentDetails {
        val tenant = tenants.currentTenantId()
        val wasRevoked = agents.get(tenant, agentId)?.row?.revokedAt != null
        return when (val revocation = agents.revoke(tenant, agentId, confirm)) {
            is AgentRevocation.Revoked -> {
                if (!wasRevoked) events.changed("agent_revoked", agentId)
                detailsOf(revocation.card)
            }

            AgentRevocation.NotFound -> {
                throw ResourceNotFound()
            }

            AgentRevocation.ConfirmationRequired -> {
                throw SelfAgentConfirmationRequired()
            }
        }
    }

    private fun connectivityOf(status: AgentStatus): Connectivity =
        when (status) {
            AgentStatus.ONLINE -> Connectivity.ONLINE
            AgentStatus.OFFLINE -> Connectivity.OFFLINE
        }

    private fun statusOf(row: AgentRow) = if (agents.online(row)) AgentStatus.ONLINE else AgentStatus.OFFLINE

    private fun summaryOf(
        row: AgentRow,
        certificate: CurrentCertificate?,
        repositories: List<AgentRepository>,
    ) = AgentSummary(
        row.id,
        row.hostname,
        statusOf(row),
        row.agentVersion,
        row.os,
        row.arch,
        row.registeredAt,
        row.lastSeenAt,
        row.revokedAt,
        row.duplicateSessionAt,
        versions.outdated(row.agentVersion),
        row.builtin,
        row.role.toApi(),
        certificate?.toApi(clock.instant()),
        repositories,
    )

    private fun detailsOf(card: AgentCard): AgentDetails {
        val row = card.row
        val certificate = certificates.current(tenants.currentTenantId(), listOf(row.id))[row.id]
        return AgentDetails(
            row.id,
            row.hostname,
            statusOf(row),
            row.agentVersion,
            row.os,
            row.arch,
            row.registeredAt,
            row.lastSeenAt,
            row.revokedAt,
            row.duplicateSessionAt,
            card.protocolVersion,
            card.plugins.map(::pluginOf),
            card.repositories.map { it.toApi() },
            card.secretNames,
            card.scriptNames,
            versions.outdated(row.agentVersion),
            row.builtin,
            row.role.toApi(),
            card.restoreDir,
            certificate?.toApi(clock.instant()),
            verifierSetupOf(row),
        )
    }

    /** A verifier's card says what its sources hold that it does not; a source has no such block. */
    private fun verifierSetupOf(row: AgentRow): List<VerifierSetupEntry>? =
        when (row.role) {
            DomainRole.SOURCE -> null
            DomainRole.VERIFIER -> repositories.verifierSetup(tenants.currentTenantId(), row.id).map { it.toApi() }
        }

    private fun pluginOf(plugin: AgentPluginView) =
        AgentPlugin(
            plugin.name,
            plugin.version,
            plugin.actions.map { StepAction.valueOf(it.uppercase()) },
            schemaOf(mapper.readTree(plugin.configSchema)),
        )

    /** A config schema is a JSON object; anything else a host announced has no fields to show. */
    private fun schemaOf(node: JsonNode) = if (node.isObject) mapper.convertValue(node, SCHEMA) else emptyMap()

    private companion object {
        val SCHEMA = object : tools.jackson.core.type.TypeReference<Map<String, Any?>>() {}
    }
}
