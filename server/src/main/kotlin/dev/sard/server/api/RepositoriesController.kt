// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.tags.Tag
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import java.time.Instant
import java.util.UUID

@Schema(description = "An agent that reports a repository, under the name it gives it")
data class RepositoryHolderInfo(
    val agentId: UUID,
    val hostname: String,
    val role: AgentRole,
    @field:Schema(description = "The name of the repository on that agent")
    val repositoryName: String,
)

@Schema(description = "A restic repository by its id, with what the agents of the tenant say about its recovery key")
data class RepositoryInfo(
    @field:Schema(description = "restic repository id")
    val repositoryId: String,
    @field:Schema(
        description =
            "From the latest Register among the holders that know the answer (ties: the smaller agent id); " +
                "unknown when none does, which is not absent",
    )
    val recoveryKey: RecoveryKeyFlag,
    @field:Schema(description = "When the Register that decided recoveryKey was received; null when unknown")
    val reportedAt: Instant?,
    @field:Schema(description = "The agent whose Register decided recoveryKey; null when unknown")
    val reportedByAgentId: UUID?,
    @field:Schema(
        description =
            "Run on the host of a source that holds the repository to issue a recovery key, in a terminal; " +
                "only when recoveryKey is absent and a source holds it, else null. No key passes through the server",
    )
    val recoveryKeyCommand: String?,
    @field:Schema(description = "Every not revoked agent that reports the repository, by agent id")
    val holders: List<RepositoryHolderInfo>,
)

@Schema(description = "The repositories of the tenant that its agents report")
data class RepositoryList(
    @field:Schema(description = "By repository id; a repository without an id is not listed")
    val items: List<RepositoryInfo>,
    @field:Schema(description = "How many items have recoveryKey absent; unknown ones are not counted")
    val withoutRecoveryKey: Int,
)

/** What the repositories endpoint does (V3). */
interface RepositoriesApi {
    fun listRepositories(): RepositoryList
}

@RestController
@RequestMapping("/api/v1/repositories", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "repositories")
class RepositoriesController(
    private val api: RepositoriesApi,
) {
    @GetMapping
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Repositories and their recovery keys",
        description =
            "One item per repository id that a not revoked agent of the tenant reports, by repository id. " +
                "Only the flag is known to the server: no key, password, key id or label (ADR 0008).",
    )
    fun listRepositories(): RepositoryList = api.listRepositories()
}
