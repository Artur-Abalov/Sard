// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.TenantResolver
import dev.sard.server.fleet.AgentPresence
import dev.sard.server.runs.AgentRevoked
import dev.sard.server.runs.RestoreAgentInvalid
import dev.sard.server.runs.RestoreConfigInvalid
import dev.sard.server.runs.RestoreDraft
import dev.sard.server.runs.Restores
import dev.sard.server.runs.SnapshotNotFound
import dev.sard.server.runs.SnapshotNotRestorable
import dev.sard.server.runs.UnknownPlugin
import dev.sard.server.runs.UnknownRepository
import org.springframework.stereotype.Component
import tools.jackson.core.type.TypeReference
import tools.jackson.databind.ObjectMapper
import java.util.UUID
import dev.sard.server.runs.IneligibleAgent as DomainIneligible
import dev.sard.server.runs.IneligibleReason as DomainIneligibleReason
import dev.sard.server.runs.RestoreOptions as DomainOptions
import dev.sard.server.runs.RestorePreview as DomainPreview
import dev.sard.server.runs.RestoreSnapshot as DomainSnapshot
import dev.sard.server.runs.RestoreTarget as DomainTarget

private val OBJECT = object : TypeReference<Map<String, Any?>>() {}
private const val CONFIG_MAX_BYTES = 64 * 1024

/**
 * The restore endpoints over [Restores], in the tenant of the session;
 * whether an agent is online is the stream manager's.
 */
@Component
class RestoresApiImpl(
    private val restores: Restores,
    private val presence: AgentPresence,
    private val tenants: TenantResolver,
    private val mapper: ObjectMapper,
) : RestoresApi {
    override fun restoreOptions(
        sourceId: UUID,
        snapshotId: String,
    ): RestoreOptions = refusing { optionsOf(restores.options(tenants.currentTenantId(), sourceId, snapshotId)) }

    override fun previewRestore(
        sourceId: UUID,
        snapshotId: String,
        request: RestoreRequest,
    ): RestorePreview =
        refusing {
            previewOf(restores.preview(tenants.currentTenantId(), sourceId, snapshotId, draftOf(request)))
        }

    override fun startRestore(
        sourceId: UUID,
        snapshotId: String,
        request: RestoreRequest,
    ): Run =
        refusing {
            RunMapping.run(restores.start(tenants.currentTenantId(), sourceId, snapshotId, draftOf(request)), null)
        }

    /** The config as JSON text; what its shape must satisfy is the domain's check against the agent's schema. */
    private fun draftOf(request: RestoreRequest): RestoreDraft {
        val config = mapper.writeValueAsString(request.config ?: emptyMap<String, Any?>())
        if (config.toByteArray().size > CONFIG_MAX_BYTES) throw RequestInvalid("config", "must be at most 64 KiB")
        return RestoreDraft(request.agentId, config)
    }

    /** The domain's refusals as the contract's problems (КВ2-КВ4). */
    private fun <T> refusing(block: () -> T): T =
        try {
            block()
        } catch (_: SnapshotNotFound) {
            throw ResourceNotFound()
        } catch (e: SnapshotNotRestorable) {
            throw RequestRefused(
                ErrorCode.SNAPSHOT_NOT_RESTORABLE,
                listOf(FieldError("snapshotId", "cannot be restored from the console: ${e.reason.stored}")),
                e,
            )
        } catch (_: RestoreAgentInvalid) {
            throw RequestInvalid("agentId", "must name an agent of this tenant that can restore the snapshot")
        } catch (e: RestoreConfigInvalid) {
            throw RequestRefused(
                ErrorCode.VALIDATION_FAILED,
                e.violations.map { FieldError(it.field, it.message) },
                e,
            )
        } catch (_: AgentRevoked) {
            throw RunRefused(ErrorCode.AGENT_REVOKED)
        } catch (_: UnknownRepository) {
            throw RunRefused(ErrorCode.UNKNOWN_REPOSITORY)
        } catch (_: UnknownPlugin) {
            throw RunRefused(ErrorCode.UNKNOWN_PLUGIN)
        }

    private fun optionsOf(options: DomainOptions) =
        RestoreOptions(
            snapshotOf(options.snapshot),
            options.targets.map(::targetOf),
            options.ineligible.map(::ineligibleOf),
            AttachHint(options.attachRepositoryName, options.attachCommand),
        )

    private fun snapshotOf(snapshot: DomainSnapshot) =
        RestoreSnapshot(
            snapshot.snapshotId,
            snapshot.createdAt,
            snapshot.totalBytes,
            snapshot.repositoryName,
            snapshot.sourceName,
            snapshot.plugin,
        )

    private fun targetOf(target: DomainTarget) =
        RestoreTarget(
            target.agentId,
            target.hostname,
            presence.online(target.agentId),
            target.sourceAgent,
            target.repositoryName,
            target.restoreDir,
            mapper.readValue(target.restoreConfigSchema, OBJECT),
            mapper.readValue(target.defaults, OBJECT),
        )

    private fun ineligibleOf(agent: DomainIneligible): IneligibleAgent =
        IneligibleAgent(agent.agentId, agent.hostname, reasonOf(agent.reason))

    private fun reasonOf(reason: DomainIneligibleReason) = IneligibleReason.valueOf(reason.name)

    private fun previewOf(preview: DomainPreview): RestorePreview {
        val online = presence.online(preview.agentId)
        val warnings =
            listOfNotNull(RestoreWarningCode.AGENT_OFFLINE.takeUnless { online }) +
                preview.warnings.map { RestoreWarningCode.valueOf(it.name) }
        return RestorePreview(
            preview.sourceName,
            preview.snapshotId,
            preview.createdAt,
            preview.totalBytes,
            preview.agentId,
            preview.hostname,
            online,
            preview.target,
            preview.paths,
            preview.restoreGlobals,
            warnings,
        )
    }
}
