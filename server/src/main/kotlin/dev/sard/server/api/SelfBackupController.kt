// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.media.Content
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.responses.ApiResponse
import io.swagger.v3.oas.annotations.tags.Tag
import jakarta.servlet.http.HttpServletRequest
import org.springdoc.core.annotations.ParameterObject
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.PutMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import tools.jackson.core.JsonParser
import tools.jackson.core.JsonToken
import tools.jackson.databind.DeserializationContext
import tools.jackson.databind.annotation.JsonDeserialize
import tools.jackson.databind.deser.std.StdDeserializer
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
    @field:Schema(
        description =
            "The storage address the wizard built the command for, without credentials; shown on the recovery " +
                "card. At most 1024 characters, no control characters, no user:password@",
    )
    val location: String? = null,
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

@Schema(description = "What a system source backs up, for the wizard to show before the binding; no secrets")
data class SelfBackupPlanItem(
    val role: SystemSourceRole,
    val name: String,
    val plugin: String,
    @field:Schema(description = "What goes into the backup: paths, or the database and where it is")
    val includes: List<String>,
    val excludes: List<String>,
)

@Schema(description = "A system source's last finished run")
data class SelfBackupLastRun(
    val id: UUID,
    val status: RunStatus,
    val finishedAt: Instant,
)

@Schema(description = "One of the self-backup's two system sources")
data class SelfBackupSource(
    val role: SystemSourceRole,
    val sourceId: UUID,
    val agentId: UUID,
    val repositoryName: String,
    val lastSuccessAt: Instant?,
    val lastFailureAt: Instant?,
    val lastRun: SelfBackupLastRun?,
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
    @field:Schema(description = "The worst of the warnings about the backup itself; local storage alone is ok")
    val status: SelfBackupStatus,
    @field:Schema(
        description =
            "What the console shows on every page, worst first: the sources are on a replaced agent, a source " +
                "has gone too long without a success, its last run failed, its schedule is off, the storage is local",
    )
    val warnings: List<SelfBackupWarningCode>,
    @field:Schema(description = "When a system source went overdue: the earliest deadline it missed; null if none")
    val overdueSince: Instant?,
    @field:Schema(
        description =
            "The repositories of the live built-in agent's last Register, by name, bound or not; empty without one",
    )
    val repositories: List<SelfBackupRepository>,
    @field:Schema(description = "What the system sources back up, whether configured or not")
    val plan: List<SelfBackupPlanItem>,
    @field:Schema(
        description =
            "Both system sources backed up successfully with runs queued at or after boundAt; false until configured",
    )
    val firstBackupDone: Boolean,
    @field:Schema(
        description =
            "When the key copy of the current binding was confirmed; null if not, or if confirmed before boundAt",
    )
    val keysConfirmedAt: Instant?,
    @field:Schema(
        description = "The command that prints the repository password on the server's machine; null until configured",
    )
    val passwordRevealCommand: String?,
    @field:Schema(
        description =
            "The command that issues a recovery key for the bound repository on the server's machine, in a terminal, " +
                "and then restarts the built-in agent so that the server learns the flag; repeating it changes " +
                "nothing. Null until configured",
    )
    val recoveryKeyCommand: String?,
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

/** A boolean that only a JSON true or false is: the string "true" is not one, whatever the mapper would coerce. */
class StrictBooleanDeserializer : StdDeserializer<Boolean>(Boolean::class.java) {
    override fun deserialize(
        parser: JsonParser,
        context: DeserializationContext,
    ): Boolean =
        when (parser.currentToken()) {
            JsonToken.VALUE_TRUE -> true
            JsonToken.VALUE_FALSE -> false
            else -> context.handleUnexpectedToken(Boolean::class.java, parser) as Boolean
        }
}

@Schema(description = "The owner's confirmation that the repository password and the storage access are kept")
data class KeysConfirmationInput(
    @field:Schema(description = "Must be true: a copy of the repository password and of the storage access is kept")
    @field:JsonDeserialize(using = StrictBooleanDeserializer::class)
    val copiedOutsideServer: Boolean? = null,
)

@Schema(description = "What the wizard asks for to build the command that adds a repository; none is a secret")
data class RepositoryCommandQuery(
    @field:Schema(description = "s3, sftp or local")
    val storage: String? = null,
    @field:Schema(description = "s3: aws, b2 or custom")
    val provider: String? = null,
    @field:Schema(description = "s3 aws and b2: the region")
    val region: String? = null,
    @field:Schema(description = "s3 custom: http or https, host and optional port")
    val endpoint: String? = null,
    @field:Schema(description = "s3: the bucket")
    val bucket: String? = null,
    @field:Schema(description = "s3: the prefix in the bucket; sftp: the absolute path")
    val path: String? = null,
    @field:Schema(description = "sftp: the user on the storage host")
    val user: String? = null,
    @field:Schema(description = "sftp: the storage host")
    val host: String? = null,
    @field:Schema(description = "sftp: the port, 22 when absent")
    val port: String? = null,
    @field:Schema(description = "sftp: SHA256:... of the host key, if the owner knows it")
    val hostKeyFingerprint: String? = null,
) {
    fun asMap(): Map<String, String> {
        val all =
            mapOf(
                "storage" to storage,
                "provider" to provider,
                "region" to region,
                "endpoint" to endpoint,
                "bucket" to bucket,
                "path" to path,
                "user" to user,
                "host" to host,
                "port" to port,
                "hostKeyFingerprint" to hostKeyFingerprint,
            )
        return all.entries.mapNotNull { (name, value) -> value?.let { name to it } }.toMap()
    }
}

@Schema(description = "The commands for a terminal on the server's machine that add a repository to the built-in agent")
data class RepositoryCommand(
    @field:Schema(description = "The name the repository will have: the first free of self, self-2, ...")
    val repositoryName: String,
    @field:Schema(description = "The address of the repository, without credentials")
    val address: String,
    @field:Schema(description = "In the order to run them; the key id and the secret key are asked in the terminal")
    val commands: List<String>,
)

@Schema(description = "What the owner needs to restore from the self-backup; no password, no credentials")
data class RecoveryCard(
    val repositoryName: String,
    @field:Schema(description = "restic backend scheme: s3, sftp, local, ...")
    val backend: String,
    @field:Schema(description = "The storage address the wizard recorded; null when none was")
    val location: String?,
    @field:Schema(description = "restic repository id; null until the agent reads it")
    val repositoryId: String?,
    @field:Schema(description = "SHA-256 fingerprint of the server's CA")
    val caFingerprint: String,
    val sardVersion: String,
    val generatedAt: Instant,
    @field:Schema(description = "Where the restore procedure is described, for this version of the server")
    val recoveryProcedure: String,
)

/** What the self-backup endpoints do (F6, F4b). */
interface SelfBackupApi {
    fun getSelfBackup(): SelfBackup

    fun bindSelfBackupRepository(input: SelfBackupRepositoryInput): SelfBackup

    fun startSelfBackup(): SelfBackupRuns

    fun confirmKeys(
        input: KeysConfirmationInput,
        clientAddress: String,
    )

    fun repositoryCommand(query: RepositoryCommandQuery): RepositoryCommand

    fun recoveryCard(): RecoveryCard
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

    @PostMapping("/keys-confirmation", consumes = [MediaType.APPLICATION_JSON_VALUE])
    @ResponseStatus(HttpStatus.NO_CONTENT)
    @Operation(
        summary = "Confirm that the repository password and the storage access are kept outside the server",
        description =
            "Does the step keys_confirmed; the first confirmation ends the onboarding. Repeating it changes " +
                "nothing. 409 self_backup_not_configured before a repository is bound; 409 first_backup_pending " +
                "before both system sources backed up after the binding; 422 validation_failed unless " +
                "copiedOutsideServer is true.",
    )
    @ApiResponse(
        responseCode = "409",
        description = "self_backup_not_configured or first_backup_pending",
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    @Unprocessable
    fun confirmKeys(
        @RequestBody input: KeysConfirmationInput,
        httpRequest: HttpServletRequest,
    ) = api.confirmKeys(input, httpRequest.remoteAddr)

    @GetMapping("/repository-command")
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "The commands that add a repository to the built-in agent",
        description =
            "Built from parameters that are no secrets; nothing is written. The key id and the secret key of an " +
                "S3 storage are asked in the terminal. 422 validation_failed names the field; 422 " +
                "self_agent_missing without a built-in agent.",
    )
    @Unprocessable
    fun repositoryCommand(
        @ParameterObject query: RepositoryCommandQuery,
    ): RepositoryCommand = api.repositoryCommand(query)

    @GetMapping("/recovery-card")
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "The recovery card",
        description = "Where the storage is and which CA the server has, to keep with the repository password.",
    )
    @ApiResponse(
        responseCode = "409",
        description = "self_backup_not_configured",
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    fun recoveryCard(): RecoveryCard = api.recoveryCard()
}
