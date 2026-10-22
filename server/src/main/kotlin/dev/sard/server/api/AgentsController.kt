// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.Parameter
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.tags.Tag
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PathVariable
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RequestParam
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import java.time.Instant
import java.util.UUID

private const val DUPLICATE_SESSION_AT =
    "When the server last confirmed a duplicate session, two hosts with one certificate (ADR 0026); " +
        "null if never. Not cleared by the server"

private const val OUTDATED =
    "True when the agent and the server's packages are both releases (vX.Y.Z) and the agent's version is lower " +
        "by semver; false when it is newer, unknown or not a release"

private const val BUILTIN =
    "True for the agent next to the server (sard-self), enrolled with the token the server wrote for it; " +
        "revoking it needs confirm=sard-self"

private const val ROLE =
    "source runs backups, restores and scripts; verifier only verifies restores. Chosen by the token, never changes"

private const val CERTIFICATE =
    "The agent's current certificate: the last one that is not revoked, not superseded and confirmed (a renewal " +
        "the agent has not confirmed does not count); null for a revoked agent and for an agent without one"

@Schema(description = "The current certificate of an agent; no key, no serial, nothing but the dates")
data class AgentCertificate(
    @field:Schema(description = "When the certificate became valid: its notBefore")
    val notBefore: Instant,
    val notAfter: Instant,
    @field:Schema(description = "Whole days to notAfter, rounded down, never negative")
    val daysLeft: Int,
    @field:Schema(
        description =
            "expired: nothing is left. expiring: at most 30/365 of its lifetime is left. renewal_due: two thirds " +
                "of its lifetime have passed and no confirmed new certificate exists. ok: otherwise",
    )
    val state: AgentCertificateState,
    @field:Schema(description = "When a renewal was issued that the agent has not confirmed; null if none")
    val renewalIssuedAt: Instant?,
)

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
    @field:Schema(description = DUPLICATE_SESSION_AT)
    val duplicateSessionAt: Instant?,
    @field:Schema(description = OUTDATED)
    val outdated: Boolean,
    @field:Schema(description = BUILTIN)
    val builtin: Boolean,
    @field:Schema(description = ROLE)
    val role: AgentRole,
    @field:Schema(description = CERTIFICATE)
    val certificate: AgentCertificate?,
    @field:Schema(description = "The repositories of the last Register, by name; empty before the first one")
    val repositories: List<AgentRepository>,
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
    @field:Schema(
        description =
            "Whether the repository has a recovery key, from the agent's last Register; unknown for an agent older " +
                "than V3 or one that could not read the keys (not the same as absent). Only the flag, never a key",
    )
    val recoveryKey: RecoveryKeyFlag,
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
    @field:Schema(description = DUPLICATE_SESSION_AT)
    val duplicateSessionAt: Instant?,
    @field:Schema(description = "Protocol version of the last Register; null until the first one")
    val protocolVersion: Int?,
    val plugins: List<AgentPlugin>,
    val repositories: List<AgentRepository>,
    @field:Schema(description = "Names of secrets defined on the host; values never leave it (ADR 0008)")
    val secretNames: List<String>,
    @field:Schema(description = "Names of allowlisted scripts on the host; paths never leave it (ADR 0008)")
    val scriptNames: List<String>,
    @field:Schema(description = OUTDATED)
    val outdated: Boolean,
    @field:Schema(description = BUILTIN)
    val builtin: Boolean,
    @field:Schema(description = ROLE)
    val role: AgentRole,
    @field:Schema(
        description =
            "The directory the host's operator allows restores into (restore.dir of the agent); the console " +
                "cannot change it. null for an agent that cannot restore from the console",
    )
    val restoreDir: String?,
    @field:Schema(description = CERTIFICATE)
    val certificate: AgentCertificate?,
    @field:Schema(
        description =
            "For a verifier: the repositories its sources hold and it does not, with the commands to give it a key; " +
                "empty when there is nothing to do. Null for a source",
    )
    val verifierSetup: List<VerifierSetupEntry>?,
)

@Schema(description = "What a verifier needs to hold a repository that the sources hold: whom to ask and what to run")
data class VerifierSetupEntry(
    val repositoryId: String,
    @field:Schema(
        description = "The source that holds the repository with the latest Register, the smaller agent id on a tie",
    )
    val sourceAgentId: UUID,
    val sourceHostname: String,
    @field:Schema(description = "The name of the repository on that source")
    val repositoryName: String,
    @field:Schema(
        description =
            "Run on the source: issues a key for this verifier and prints the repo add command with the address. " +
                "Null when the name cannot be put into a command safely (a quote or a control character)",
    )
    val keyAddCommand: String?,
    @field:Schema(
        description =
            "Run on the verifier: the repo add command with the literal placeholders <address> and <file>, " +
                "which the key add command fills in; the server does not know them (ADR 0008). Null as keyAddCommand",
    )
    val repoAddTemplate: String?,
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

    fun revokeAgent(
        agentId: UUID,
        confirm: String?,
    ): AgentDetails
}

@RestController
@RequestMapping("/api/v1/agents", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "agents")
class AgentsController(
    private val api: AgentsApi,
) {
    @GetMapping
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "List agents", description = "Newest first.")
    @Unprocessable
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

    @PostMapping("/{agentId}/revoke")
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Revoke an agent",
        description =
            "Revokes all its certificates and closes its open stream at once; its queued, dispatched and running " +
                "steps become lost. Its history stays. Revoking a revoked agent is a no-op and answers 200. " +
                "A live built-in agent is revoked only with confirm=sard-self, else 409 and nothing changes; " +
                "other agents ignore confirm.",
    )
    @NotFound
    @ConfirmationRequired
    fun revokeAgent(
        @PathVariable agentId: UUID,
        @Parameter(description = "sard-self, exactly, to revoke a live built-in agent")
        @RequestParam(required = false)
        confirm: String?,
    ): AgentDetails = api.revokeAgent(agentId, confirm)
}
