// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.TenantResolver
import dev.sard.server.persistence.PageKey
import dev.sard.server.runs.AgentRevoked
import dev.sard.server.runs.Runs
import dev.sard.server.runs.SnapshotView
import dev.sard.server.runs.Snapshots
import dev.sard.server.runs.SourceDraft
import dev.sard.server.runs.SourceView
import dev.sard.server.runs.Sources
import dev.sard.server.runs.UnknownPlugin
import dev.sard.server.runs.UnknownRepository
import org.springframework.stereotype.Component
import tools.jackson.core.type.TypeReference
import tools.jackson.databind.ObjectMapper
import java.util.UUID

private const val NAME_MAX = 200
private const val CONFIG_MAX_BYTES = 64 * 1024
private val CONFIG = object : TypeReference<Map<String, Any?>>() {}

/** The source endpoints over [Sources] and [Runs], in the tenant of the session. */
@Component
class SourcesApiImpl(
    private val sources: Sources,
    private val runs: Runs,
    private val snapshots: Snapshots,
    private val tenants: TenantResolver,
    private val mapper: ObjectMapper,
) : SourcesApi {
    override fun listSources(
        agentId: UUID?,
        cursor: String?,
        limit: Int,
    ): SourcePage {
        val page = pageRequest(CursorKind.SOURCES, cursor, limit)
        val rows = sources.list(tenants.currentTenantId(), agentId, page.after, page.fetch)
        val slice = page.slice(rows) { PageKey(it.createdAt, it.id) }
        return SourcePage(slice.items.map(::sourceOf), slice.nextCursor)
    }

    override fun createSource(source: SourceInput): Source = sourceOf(sources.create(tenants.currentTenantId(), draftOf(source)))

    override fun getSource(sourceId: UUID): Source = sourceOf(sources.get(tenants.currentTenantId(), sourceId))

    override fun replaceSource(
        sourceId: UUID,
        source: SourceInput,
    ): Source = sourceOf(sources.replace(tenants.currentTenantId(), sourceId, draftOf(source)))

    override fun deleteSource(sourceId: UUID) = sources.delete(tenants.currentTenantId(), sourceId)

    /** A run cannot start for an agent that is revoked or no longer offers the plugin or repository (S8b В6): 409. */
    override fun startRun(sourceId: UUID): Run =
        try {
            RunMapping.run(runs.start(tenants.currentTenantId(), sourceId))
        } catch (_: AgentRevoked) {
            throw RunRefused(ErrorCode.AGENT_REVOKED)
        } catch (_: UnknownPlugin) {
            throw RunRefused(ErrorCode.UNKNOWN_PLUGIN)
        } catch (_: UnknownRepository) {
            throw RunRefused(ErrorCode.UNKNOWN_REPOSITORY)
        }

    override fun listSourceSnapshots(
        sourceId: UUID,
        cursor: String?,
        limit: Int,
    ): SnapshotPage {
        val page = pageRequest(CursorKind.SNAPSHOTS, cursor, limit)
        val rows = snapshots.ofSource(tenants.currentTenantId(), sourceId, page.after, page.fetch) ?: throw ResourceNotFound()
        val slice = page.slice(rows) { PageKey(it.createdAt, it.id) }
        return SnapshotPage(slice.items.map(::snapshotOf), slice.nextCursor)
    }

    private fun snapshotOf(view: SnapshotView) =
        Snapshot(
            view.id,
            view.snapshotId,
            view.sourceId,
            view.runId,
            view.stepId,
            view.agentId,
            view.repositoryName,
            view.repositoryId,
            view.totalBytes,
            view.addedBytes,
            view.createdAt,
            view.forgottenAt,
            view.partial,
        )

    /** What the request itself must satisfy; whether the agent offers it is the domain's check. */
    private fun draftOf(input: SourceInput): SourceDraft {
        if (input.name.codePointCount(0, input.name.length) !in 1..NAME_MAX) {
            throw RequestInvalid("name", "must be 1 to $NAME_MAX characters")
        }
        val config = mapper.writeValueAsString(input.config)
        if (config.toByteArray().size > CONFIG_MAX_BYTES) throw RequestInvalid("config", "must be at most 64 KiB")
        return SourceDraft(input.name, input.agentId, input.plugin, input.repositoryName, config)
    }

    private fun sourceOf(view: SourceView) =
        Source(
            view.id,
            view.name,
            view.agentId,
            view.plugin,
            view.repositoryName,
            mapper.readValue(view.config, CONFIG),
            view.createdAt,
            view.updatedAt,
        )
}
