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
import org.springframework.web.bind.annotation.DeleteMapping
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PathVariable
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.PutMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RequestParam
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import java.time.Instant
import java.util.UUID

private const val REPOSITORY_NAME = "A repository from the agent's last Register; its snapshots live there"
private const val CONFIG = "Plugin config, valid against the plugin's configSchema; secrets by name only"

@Schema(description = "What to back up, where from and where to")
data class SourceInput(
    val name: String,
    val agentId: UUID,
    @field:Schema(description = "A plugin the agent offers")
    val plugin: String,
    @field:Schema(description = REPOSITORY_NAME)
    val repositoryName: String,
    @field:Schema(description = CONFIG)
    val config: Map<String, Any?>,
) {
    // Spring logs the bodies it reads and writes at DEBUG: never the config (its values stay out of the log).
    override fun toString() = "SourceInput(name=$name, agentId=$agentId, plugin=$plugin)"
}

@Schema(description = "A backup source")
data class Source(
    val id: UUID,
    val name: String,
    val agentId: UUID,
    val plugin: String,
    @field:Schema(description = REPOSITORY_NAME)
    val repositoryName: String,
    @field:Schema(description = CONFIG)
    val config: Map<String, Any?>,
    val createdAt: Instant,
    val updatedAt: Instant,
) {
    override fun toString() = "Source(id=$id, name=$name, agentId=$agentId, plugin=$plugin)"
}

@Schema(description = "A page of sources")
data class SourcePage(
    val items: List<Source>,
    @field:Schema(description = CURSOR_NEXT)
    val nextCursor: String?,
)

@Schema(description = "A restic snapshot made by a run of the source")
data class Snapshot(
    val id: UUID,
    @field:Schema(description = "restic snapshot id")
    val snapshotId: String,
    val sourceId: UUID,
    val runId: UUID,
    val stepId: UUID,
    val agentId: UUID,
    val repositoryName: String,
    @field:Schema(description = "restic repository id; the same key held by several hosts shares it (ADR 0008)")
    val repositoryId: String,
    val totalBytes: Long,
    val addedBytes: Long,
    val createdAt: Instant,
    @field:Schema(description = "When restic forget removed it; null while it exists")
    val forgottenAt: Instant?,
    @field:Schema(description = "The backup failed after saving it (some files unreadable): usable, but incomplete")
    val partial: Boolean,
)

@Schema(description = "A page of snapshots")
data class SnapshotPage(
    val items: List<Snapshot>,
    @field:Schema(description = CURSOR_NEXT)
    val nextCursor: String?,
)

/** What the source endpoints do; S8b implements it. */
interface SourcesApi {
    fun listSources(
        agentId: UUID?,
        cursor: String?,
        limit: Int,
    ): SourcePage

    fun createSource(source: SourceInput): Source

    fun getSource(sourceId: UUID): Source

    fun replaceSource(
        sourceId: UUID,
        source: SourceInput,
    ): Source

    fun deleteSource(sourceId: UUID)

    fun startRun(sourceId: UUID): Run

    fun listSourceSnapshots(
        sourceId: UUID,
        cursor: String?,
        limit: Int,
    ): SnapshotPage
}

@RestController
@RequestMapping("/api/v1/sources", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "sources")
class SourcesController(
    private val api: SourcesApi,
) {
    @GetMapping
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "List sources", description = "Newest first.")
    @Unprocessable
    fun listSources(
        @RequestParam(required = false) agentId: UUID?,
        @PageCursor @RequestParam(required = false) cursor: String?,
        @PageLimit @RequestParam(defaultValue = DEFAULT_LIMIT) limit: Int,
    ): SourcePage = api.listSources(agentId, cursor, limit)

    @PostMapping(consumes = [MediaType.APPLICATION_JSON_VALUE])
    @ResponseStatus(HttpStatus.CREATED)
    @Operation(
        summary = "Create a source",
        description =
            "422 codes: unknown_agent, agent_revoked, unknown_plugin (or the plugin does not offer backup), " +
                "unknown_repository, invalid_config " +
                "(errors name config/<JSON Pointer>), validation_failed.",
    )
    @Unprocessable
    fun createSource(
        @RequestBody source: SourceInput,
    ): Source = api.createSource(source)

    @GetMapping("/{sourceId}")
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "Source card")
    @NotFound
    fun getSource(
        @PathVariable sourceId: UUID,
    ): Source = api.getSource(sourceId)

    @PutMapping("/{sourceId}", consumes = [MediaType.APPLICATION_JSON_VALUE])
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Replace a source",
        description = "The same 422 codes as creation, unknown_plugin included when the plugin does not offer backup.",
    )
    @NotFound
    @Unprocessable
    fun replaceSource(
        @PathVariable sourceId: UUID,
        @RequestBody source: SourceInput,
    ): Source = api.replaceSource(sourceId, source)

    @DeleteMapping("/{sourceId}")
    @ResponseStatus(HttpStatus.NO_CONTENT)
    @Operation(summary = "Delete a source", description = "Its runs and snapshots stay in the history.")
    @ApiResponse(responseCode = "204", description = "Deleted")
    @NotFound
    @RunActive
    fun deleteSource(
        @PathVariable sourceId: UUID,
    ): Unit = api.deleteSource(sourceId)

    @PostMapping("/{sourceId}/runs")
    @ResponseStatus(HttpStatus.CREATED)
    @Operation(
        summary = "Back up the source now",
        description =
            "Queues a manual backup run; it waits for an offline agent. One active run per source (D6). 409 " +
                "also when the agent is revoked (agent_revoked) or its last Register no longer offers the " +
                "plugin (unknown_plugin) or the repository (unknown_repository).",
    )
    @NotFound
    @ApiResponse(
        responseCode = "409",
        description = "The source has an active run (activeRunId), or the run cannot start (code)",
        content = [
            Content(
                mediaType = PROBLEM_JSON,
                schema = Schema(anyOf = [RunActiveProblem::class, Problem::class]),
            ),
        ],
    )
    fun startRun(
        @PathVariable sourceId: UUID,
    ): Run = api.startRun(sourceId)

    @GetMapping("/{sourceId}/snapshots")
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "Snapshots of the source", description = "Newest first; also of a deleted source.")
    @NotFound
    @Unprocessable
    fun listSourceSnapshots(
        @PathVariable sourceId: UUID,
        @PageCursor @RequestParam(required = false) cursor: String?,
        @PageLimit @RequestParam(defaultValue = DEFAULT_LIMIT) limit: Int,
    ): SnapshotPage = api.listSourceSnapshots(sourceId, cursor, limit)
}
