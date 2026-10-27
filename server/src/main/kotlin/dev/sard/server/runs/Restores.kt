// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.RunRecord
import dev.sard.server.persistence.RunStepRecord
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import jakarta.persistence.LockModeType
import org.hibernate.Session
import tools.jackson.databind.json.JsonMapper
import java.time.Clock
import java.util.UUID

private val JSON = JsonMapper.builder().build()

/**
 * Restore from the console (R2a): which agents can restore a snapshot, what a request would do, and the run
 * that does it. A restore is a run of its own kind (ВК34): one `restore` step on the chosen agent, which
 * reaches the repository by the name it has for it; it blocks no backup and is blocked by none (ВК35).
 * The server decides only what it knows: the snapshot, the agents' last Register and the config's shape;
 * whether the destination is free is the agent's to say.
 */
class Restores(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val ids: UuidV7,
    private val queued: StepsQueued,
) {
    /** The options of restoring snapshot [snapshotId] (restic's id) of [sourceId], deleted or not. */
    fun options(
        tenantId: UUID,
        sourceId: UUID,
        snapshotId: String,
    ): RestoreOptions =
        sessions.inTenant(tenantId) { session ->
            val (source, snapshot) = RestoreChoice.snapshotOf(session, sourceId, snapshotId)
            RestoreTargets.of(session, source, snapshot)
        }

    /** What [draft] would do; the same refusals as [start], nothing is created. */
    fun preview(
        tenantId: UUID,
        sourceId: UUID,
        snapshotId: String,
        draft: RestoreDraft,
    ): RestorePreview =
        sessions.inTenant(tenantId) { session ->
            val chosen = RestoreChoice.resolve(session, sourceId, snapshotId, draft, LockModeType.NONE)
            RestorePreview(
                sourceName = chosen.source.name,
                snapshotId = chosen.snapshot.snapshotId,
                createdAt = chosen.snapshot.createdAt,
                totalBytes = chosen.snapshot.totalBytes,
                agentId = chosen.agent.id,
                hostname = chosen.agent.hostname,
                target = chosen.target,
                paths = chosen.paths,
                restoreGlobals = chosen.restoreGlobals,
                warnings = chosen.warnings,
            )
        }

    /** Queues the restore: a run of kind restore on the chosen agent, which gets its step when it is online. */
    fun start(
        tenantId: UUID,
        sourceId: UUID,
        snapshotId: String,
        draft: RestoreDraft,
    ): RunView {
        val run =
            sessions.inTenant(tenantId) { session ->
                val chosen = RestoreChoice.resolve(session, sourceId, snapshotId, draft, LockModeType.PESSIMISTIC_READ)
                create(session, chosen)
            }
        queued.onQueued(tenantId, run.agentId)
        return run
    }

    private fun create(
        session: Session,
        chosen: RestoreChoice,
    ): RunView {
        val now = clock.instant()
        val run =
            RunRecord(
                ids.next(),
                chosen.source.id,
                Trigger.MANUAL.stored,
                RunState.QUEUED.stored,
                now,
                null,
                kind = RunKind.RESTORE.stored,
                requestedBy = ADMIN,
                restore = restoreJson(chosen),
            )
        val step =
            RunStepRecord(
                id = ids.next(),
                runId = run.id,
                ordinal = 0,
                agentId = chosen.agent.id,
                sourceId = chosen.source.id,
                plugin = chosen.source.plugin,
                action = Action.RESTORE.stored,
                repositoryName = chosen.repositoryName,
                config = chosen.config.toString(),
                status = StepState.QUEUED.stored,
                queuedAt = now,
                snapshotId = chosen.snapshot.snapshotId,
                timeoutSeconds = timeoutOf(session, chosen) * SECONDS_PER_MINUTE,
            )
        session.persist(run)
        session.persist(step)
        session.flush()
        return RunViews.of(run, chosen.agent.id, chosen.source, listOf(step))
    }

    /** The restore timeout of the source in minutes, else the tenant's default (J1b). */
    private fun timeoutOf(
        session: Session,
        chosen: RestoreChoice,
    ): Int =
        chosen.source.restoreTimeoutMinutes
            ?: TimeoutSettings.read(session, checkNotNull(chosen.source.tenantId)).restoreMinutes

    private fun restoreJson(chosen: RestoreChoice): String =
        JSON.writeValueAsString(
            mapOf(
                "snapshotId" to chosen.snapshot.snapshotId,
                "snapshotCreatedAt" to chosen.snapshot.createdAt.toString(),
                "repositoryName" to chosen.repositoryName,
                "config" to chosen.config,
                "target" to chosen.target,
            ),
        )
}
