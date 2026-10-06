// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.TenantResolver
import dev.sard.server.fleet.AgentCard
import dev.sard.server.fleet.AgentPluginView
import dev.sard.server.fleet.AgentRow
import dev.sard.server.fleet.Agents
import dev.sard.server.fleet.Connectivity
import dev.sard.server.install.AgentVersions
import dev.sard.server.persistence.PageKey
import org.springframework.stereotype.Component
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.util.UUID

/** The agent endpoints over [Agents], in the tenant of the session. */
@Component
class AgentsApiImpl(
    private val agents: Agents,
    private val tenants: TenantResolver,
    private val mapper: ObjectMapper,
    private val versions: AgentVersions,
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
        return AgentPage(slice.items.map(::summaryOf), slice.nextCursor)
    }

    override fun getAgent(agentId: UUID): AgentDetails =
        detailsOf(agents.get(tenants.currentTenantId(), agentId) ?: throw ResourceNotFound())

    override fun revokeAgent(agentId: UUID): AgentDetails =
        detailsOf(agents.revoke(tenants.currentTenantId(), agentId) ?: throw ResourceNotFound())

    private fun connectivityOf(status: AgentStatus): Connectivity =
        when (status) {
            AgentStatus.ONLINE -> Connectivity.ONLINE
            AgentStatus.OFFLINE -> Connectivity.OFFLINE
        }

    private fun statusOf(row: AgentRow) = if (agents.online(row)) AgentStatus.ONLINE else AgentStatus.OFFLINE

    private fun summaryOf(row: AgentRow) =
        AgentSummary(
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
        )

    private fun detailsOf(card: AgentCard): AgentDetails {
        val row = card.row
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
            card.repositories.map {
                AgentRepository(
                    it.name,
                    it.backend,
                    it.repositoryId,
                    it.cryptoProvider,
                )
            },
            card.secretNames,
            card.scriptNames,
            versions.outdated(row.agentVersion),
        )
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
