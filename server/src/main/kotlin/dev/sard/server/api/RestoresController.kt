// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.media.Content
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.responses.ApiResponse
import io.swagger.v3.oas.annotations.tags.Tag
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PathVariable
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import java.time.Instant
import java.util.UUID

private const val SNAPSHOT_ID = "restic snapshot id"

@Schema(description = "The snapshot to restore")
data class RestoreSnapshot(
    @field:Schema(description = SNAPSHOT_ID)
    val snapshotId: String,
    val createdAt: Instant,
    val totalBytes: Long,
    @field:Schema(description = "The repository the snapshot was made in, by the name of the agent that made it")
    val repositoryName: String,
    val sourceName: String,
    val plugin: String,
)

@Schema(description = "An agent that can restore the snapshot")
data class RestoreTarget(
    val agentId: UUID,
    val hostname: String,
    val online: Boolean,
    @field:Schema(description = "The agent that made the snapshot")
    val sourceAgent: Boolean,
    @field:Schema(description = "The repository on this agent that holds the snapshot, by this agent's name for it")
    val repositoryName: String,
    @field:Schema(
        description =
            "restore.dir of the agent: every restore goes into a new directory inside it; " +
                "null when the plugin restores elsewhere (a database)",
    )
    val restoreDir: String?,
    @field:Schema(description = "JSON Schema (draft 2020-12) of the restore config this agent accepts")
    val restoreConfigSchema: Map<String, Any?>,
    @field:Schema(description = "The config the form starts from")
    val defaults: Map<String, Any?>,
)

@Schema(description = "An agent with the snapshot's repository that cannot restore it")
data class IneligibleAgent(
    val agentId: UUID,
    val hostname: String,
    val reason: IneligibleReason,
)

@Schema(description = "How to attach the repository on another host: placeholders, never an address or a key")
data class AttachHint(
    val repositoryName: String,
    @field:Schema(description = "A command of the agent with placeholders for the address and the copy of the password")
    val command: String,
)

@Schema(description = "Who can restore a snapshot and how")
data class RestoreOptions(
    val snapshot: RestoreSnapshot,
    val targets: List<RestoreTarget>,
    val ineligible: List<IneligibleAgent>,
    val attachHint: AttachHint,
)

@Schema(description = "Where to restore and with which config")
data class RestoreRequest(
    @field:Schema(description = "A target of the restore options")
    val agentId: String?,
    @field:Schema(description = "Valid against the target's restoreConfigSchema; secrets by name only")
    val config: Map<String, Any?>?,
) {
    // Spring logs the bodies it reads at DEBUG: the config stays out of the log.
    override fun toString() = "RestoreRequest(agentId=$agentId)"
}

@Schema(description = "What a restore request would do; nothing is created")
data class RestorePreview(
    val sourceName: String,
    @field:Schema(description = SNAPSHOT_ID)
    val snapshotId: String,
    val createdAt: Instant,
    val totalBytes: Long,
    val agentId: UUID,
    val hostname: String,
    val agentOnline: Boolean,
    @field:Schema(description = "The absolute path of the new directory, or the name of the new database")
    val target: String,
    @field:Schema(description = "The paths of the snapshot to restore; empty: the whole snapshot")
    val paths: List<String>,
    val restoreGlobals: Boolean,
    val warnings: List<RestoreWarningCode>,
)

/** What the restore endpoints do. */
interface RestoresApi {
    fun restoreOptions(
        sourceId: UUID,
        snapshotId: String,
    ): RestoreOptions

    fun previewRestore(
        sourceId: UUID,
        snapshotId: String,
        request: RestoreRequest,
    ): RestorePreview

    fun startRestore(
        sourceId: UUID,
        snapshotId: String,
        request: RestoreRequest,
    ): Run
}

private const val REFUSED =
    "The agent is revoked (agent_revoked), does not hold the snapshot's repository (unknown_repository) or cannot " +
        "restore this plugin from the console (unknown_plugin)"

@RestController
@RequestMapping("/api/v1/sources/{sourceId}/snapshots/{snapshotId}", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "restores")
class RestoresController(
    private val api: RestoresApi,
) {
    @GetMapping("/restore-options")
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Who can restore a snapshot",
        description =
            "The agents that hold the snapshot's repository and restore from the console, the source's agent " +
                "first; the ones that hold it but cannot, with the reason; " +
                "how to attach the repository to another host.",
    )
    @NotFound
    fun restoreOptions(
        @PathVariable sourceId: UUID,
        @PathVariable snapshotId: String,
    ): RestoreOptions = api.restoreOptions(sourceId, snapshotId)

    @PostMapping("/restores/preview", consumes = [MediaType.APPLICATION_JSON_VALUE])
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Preview a restore",
        description = "Checks the request like the restore itself and creates nothing.",
    )
    @NotFound
    @Unprocessable
    @ApiResponse(
        responseCode = "409",
        description = REFUSED,
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    fun previewRestore(
        @PathVariable sourceId: UUID,
        @PathVariable snapshotId: String,
        @RequestBody request: RestoreRequest,
    ): RestorePreview = api.previewRestore(sourceId, snapshotId, request)

    @PostMapping("/restores", consumes = [MediaType.APPLICATION_JSON_VALUE])
    @ResponseStatus(HttpStatus.CREATED)
    @Operation(
        summary = "Restore a snapshot",
        description =
            "Queues a restore run on the chosen agent; it waits for an offline agent. 422 snapshot_not_restorable " +
                "for a snapshot the console does not restore, validation_failed for the agent or the config " +
                "(errors name agentId or config/<JSON Pointer>). The agent decides whether the destination is free.",
    )
    @NotFound
    @Unprocessable
    @ApiResponse(
        responseCode = "409",
        description = REFUSED,
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    fun startRestore(
        @PathVariable sourceId: UUID,
        @PathVariable snapshotId: String,
        @RequestBody request: RestoreRequest,
    ): Run = api.startRestore(sourceId, snapshotId, request)
}
