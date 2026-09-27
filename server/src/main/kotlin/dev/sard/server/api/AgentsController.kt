// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.tags.Tag
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PathVariable
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RequestParam
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import java.time.Instant
import java.util.UUID

@Schema(description = "An agent in the list")
data class AgentSummary(
    val id: UUID,
    @field:Schema(description = "Host name reported at enrollment and on Register")
    val hostname: String,
    val status: AgentStatus,
    @field:Schema(description = "sard-agent version; null until the first Register")
    val agentVersion: String?,
    @field:Schema(description = "GOOS of the agent, e.g. linux; null until the first Register")
    val os: String?,
    @field:Schema(description = "GOARCH of the agent, e.g. amd64; null until the first Register")
    val arch: String?,
    val registeredAt: Instant,
    @field:Schema(description = "Last heartbeat; null if the agent never connected")
    val lastSeenAt: Instant?,
    @field:Schema(description = "When its certificates were revoked; null if not revoked")
    val revokedAt: Instant?,
)

@Schema(description = "A plugin the agent offers")
data class AgentPlugin(
    val name: String,
    val version: String,
    val actions: List<StepAction>,
    @field:Schema(description = "JSON Schema (draft 2020-12) of a source's config; secret fields hold a secret name")
    val configSchema: Map<String, Any?>,
)

@Schema(description = "A restic repository defined on the agent host (ADR 0008); no credentials")
data class AgentRepository(
    @field:Schema(description = "Name a source refers to")
    val name: String,
    @field:Schema(description = "Storage backend, e.g. local, sftp, s3")
    val backend: String,
    @field:Schema(description = "restic repository id; null while the repository is not initialized")
    val repositoryId: String?,
    @field:Schema(description = "crypto.Provider that hands the key to restic")
    val cryptoProvider: String,
)

@Schema(description = "An agent with what it reported in its last Register")
data class AgentDetails(
    val id: UUID,
    val hostname: String,
    val status: AgentStatus,
    val agentVersion: String?,
    val os: String?,
    val arch: String?,
    val registeredAt: Instant,
    val lastSeenAt: Instant?,
    val revokedAt: Instant?,
    @field:Schema(description = "Protocol version of the last Register; null until the first one")
    val protocolVersion: Int?,
    val plugins: List<AgentPlugin>,
    val repositories: List<AgentRepository>,
    @field:Schema(description = "Names of secrets defined on the host; values never leave it (ADR 0008)")
    val secretNames: List<String>,
    @field:Schema(description = "Names of allowlisted scripts on the host; paths never leave it (ADR 0008)")
    val scriptNames: List<String>,
)

@Schema(description = "A page of agents")
data class AgentPage(
    val items: List<AgentSummary>,
    @field:Schema(description = CURSOR_NEXT)
    val nextCursor: String?,
)

/** What the agent endpoints do; S8b implements it. */
interface AgentsApi {
    fun listAgents(
        status: AgentStatus?,
        cursor: String?,
        limit: Int,
    ): AgentPage

    fun getAgent(agentId: UUID): AgentDetails
}

@RestController
@RequestMapping("/api/v1/agents", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "agents")
class AgentsController(
    private val api: AgentsApi,
) {
    @GetMapping
    @Operation(summary = "List agents")
    fun listAgents(
        @RequestParam(required = false) status: AgentStatus?,
        @PageCursor @RequestParam(required = false) cursor: String?,
        @PageLimit @RequestParam(defaultValue = DEFAULT_LIMIT) limit: Int,
    ): AgentPage = api.listAgents(status, cursor, limit)

    @GetMapping("/{agentId}")
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "Agent card")
    @NotFound
    fun getAgent(
        @PathVariable agentId: UUID,
    ): AgentDetails = api.getAgent(agentId)
}
