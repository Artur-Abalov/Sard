// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.TenantResolver
import dev.sard.server.runs.AgentRevoked
import dev.sard.server.runs.UnknownPlugin
import dev.sard.server.runs.UnknownRepository
import dev.sard.server.selfbackup.SelfBackupRun
import dev.sard.server.selfbackup.SelfBackupView
import dev.sard.server.selfbackup.SelfBackups
import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.media.Content
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.responses.ApiResponse
import io.swagger.v3.oas.annotations.tags.Tag
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.stereotype.Component
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.PutMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import java.time.Instant
import java.util.UUID

@Schema(description = "The repository of the built-in agent the self-backup goes to")
data class SelfBackupRepositoryInput(
    @field:Schema(description = "A repository of the built-in agent's last Register, initialised")
    val repositoryName: String,
    @field:Schema(
        description =
            "Required true for a local repository: a copy on the server's own machine is lost with the machine",
    )
    val confirmLocalStorage: Boolean = false,
)

@Schema(description = "Where the self-backup goes, as the built-in agent's last Register describes it")
data class SelfBackupRepository(
    val name: String,
    @field:Schema(description = "restic backend scheme: s3, sftp, local, ...; empty once Register no longer lists it")
    val backend: String,
    @field:Schema(description = "restic repository id; null until the agent reads it")
    val repositoryId: String?,
    @field:Schema(description = "On the server's own machine: warn about it on every page")
    val local: Boolean,
)

@Schema(description = "One of the self-backup's two system sources")
data class SelfBackupSource(
    val role: SystemSourceRole,
    val sourceId: UUID,
    val agentId: UUID,
    val repositoryName: String,
)

@Schema(description = "The self-backup of the installation: the database, the CA and the configuration")
data class SelfBackup(
    @field:Schema(description = "A repository is bound and both system sources exist")
    val configured: Boolean,
    @field:Schema(description = "The live built-in agent (sard-self); null without one")
    val agentId: UUID?,
    @field:Schema(description = "Null until configured")
    val repository: SelfBackupRepository?,
    @field:Schema(description = "When the system sources last moved to a repository or agent; null until configured")
    val boundAt: Instant?,
    @field:Schema(description = "Database first; empty until configured")
    val sources: List<SelfBackupSource>,
)

@Schema(description = "A run of one system source")
data class SelfBackupRunStarted(
    val role: SystemSourceRole,
    val sourceId: UUID,
    val runId: UUID,
    @field:Schema(description = "False when the source already had this run active (D6): nothing new was queued")
    val started: Boolean,
)

@Schema(description = "The runs \"back up now\" follows: one per system source, database first")
data class SelfBackupRuns(
    val runs: List<SelfBackupRunStarted>,
)

/** What the self-backup endpoints do (F6). */
interface SelfBackupApi {
    fun getSelfBackup(): SelfBackup

    fun bindSelfBackupRepository(input: SelfBackupRepositoryInput): SelfBackup

    fun startSelfBackup(): SelfBackupRuns
}

@RestController
@RequestMapping("/api/v1/self-backup", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "self-backup")
class SelfBackupController(
    private val api: SelfBackupApi,
) {
    @GetMapping
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "The self-backup's state")
    fun getSelfBackup(): SelfBackup = api.getSelfBackup()

    @PutMapping("/repository", consumes = [MediaType.APPLICATION_JSON_VALUE])
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Bind the self-backup to a repository of the built-in agent",
        description =
            "Creates the two system sources with a nightly schedule, or moves them to this repository and the " +
                "current built-in agent; binding again unchanged changes nothing. 422 codes: self_agent_missing, " +
                "unknown_repository, repository_not_initialized, local_storage_unconfirmed, unknown_plugin, " +
                "invalid_config (the built-in agent lacks the database password's secret), validation_failed.",
    )
    @Unprocessable
    fun bindSelfBackupRepository(
        @RequestBody input: SelfBackupRepositoryInput,
    ): SelfBackup = api.bindSelfBackupRepository(input)

    @PostMapping("/runs")
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Back up the installation now",
        description =
            "Queues a manual run of each system source; a source with an active run answers with that run. 409 " +
                "self_backup_not_configured before a repository is bound; agent_revoked, unknown_plugin or " +
                "unknown_repository when a run cannot start.",
    )
    @ApiResponse(
        responseCode = "409",
        description = "Not configured, or a run cannot start (code)",
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    fun startSelfBackup(): SelfBackupRuns = api.startSelfBackup()
}

/** The self-backup endpoints over [SelfBackups], in the tenant of the session. */
@Component
class SelfBackupApiImpl(
    private val selfBackups: SelfBackups,
    private val tenants: TenantResolver,
) : SelfBackupApi {
    override fun getSelfBackup(): SelfBackup = selfBackupOf(selfBackups.get(tenants.currentTenantId()))

    override fun bindSelfBackupRepository(input: SelfBackupRepositoryInput): SelfBackup {
        val tenant = tenants.currentTenantId()
        return selfBackupOf(selfBackups.bind(tenant, input.repositoryName, input.confirmLocalStorage))
    }

    /** As POST /sources/{id}/runs: a run that cannot start for its agent is a conflict, not a bad request. */
    override fun startSelfBackup(): SelfBackupRuns =
        try {
            SelfBackupRuns(selfBackups.runNow(tenants.currentTenantId()).map(::runOf))
        } catch (_: AgentRevoked) {
            throw RunRefused(ErrorCode.AGENT_REVOKED)
        } catch (_: UnknownPlugin) {
            throw RunRefused(ErrorCode.UNKNOWN_PLUGIN)
        } catch (_: UnknownRepository) {
            throw RunRefused(ErrorCode.UNKNOWN_REPOSITORY)
        }

    private fun runOf(run: SelfBackupRun) =
        SelfBackupRunStarted(SystemSourceRole.valueOf(run.role.name), run.sourceId, run.runId, run.started)

    private fun selfBackupOf(view: SelfBackupView) =
        SelfBackup(
            view.configured,
            view.agentId,
            view.repository?.let { SelfBackupRepository(it.name, it.backend, it.repositoryId, it.local) },
            view.boundAt,
            view.sources.map {
                SelfBackupSource(SystemSourceRole.valueOf(it.role.name), it.sourceId, it.agentId, it.repositoryName)
            },
        )
}
