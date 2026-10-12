// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.OverdueEpisodeRecord
import dev.sard.server.persistence.RunRecord
import dev.sard.server.persistence.RunStepRecord
import dev.sard.server.persistence.SourceRecord
import dev.sard.server.runs.RunState
import dev.sard.server.runs.RunViews
import dev.sard.server.runs.Trigger
import org.hibernate.Session
import java.util.UUID

private const val FIRST_STEP = "from RunStepRecord s where s.runId = :run order by s.ordinal"

// D6 keeps one active run per source, so every run queued before this one has finished: the answer is fixed.
private const val PREVIOUS_STATUS =
    "select r.status from RunRecord r where r.sourceId = :source and r.finishedAt is not null " +
        "and (r.queuedAt < :queued or (r.queuedAt = :queued and r.id < :run)) order by r.queuedAt desc, r.id desc"

/** What a claimed delivery tells of, read back in its tenant's session at send time (S9a, F6). */
internal object Notices {
    fun of(
        session: Session,
        delivery: DueDelivery,
    ): Notice =
        delivery.runId?.let { notice(session, delivery.tenantId, it) }
            ?: overdue(session, delivery.tenantId, checkNotNull(delivery.episodeId), delivery.subject)

    private fun overdue(
        session: Session,
        tenantId: UUID,
        episodeId: UUID,
        subject: String,
    ): OverdueNotice {
        val episode = session.find(OverdueEpisodeRecord::class.java, episodeId)
        val source = session.find(SourceRecord::class.java, episode.sourceId)
        val closing = episode.closedByRunId?.takeIf { subject == SUBJECT_OVERDUE_OVER }
        return OverdueNotice(
            tenantId = tenantId,
            episodeId = episodeId,
            sourceId = source.id,
            sourceName = source.name,
            agentHostname = session.find(Agent::class.java, source.agentId).hostname,
            countedFrom = episode.countedFrom,
            deadline = episode.deadline,
            closedBy = closing?.let { notice(session, tenantId, it) },
        )
    }

    private fun notice(
        session: Session,
        tenantId: UUID,
        runId: UUID,
    ): RunNotice {
        val run = session.find(RunRecord::class.java, runId)
        val source = session.find(SourceRecord::class.java, run.sourceId)
        val step =
            session
                .createSelectionQuery(FIRST_STEP, RunStepRecord::class.java)
                .setParameter("run", runId)
                .setMaxResults(1)
                .singleResult
        val agentId = step.agentId
        val agent = session.find(Agent::class.java, agentId)
        val stepView = RunViews.step(step)
        return RunNotice(
            tenantId = tenantId,
            runId = runId,
            trigger = Trigger.of(run.trigger),
            status = RunState.of(run.status),
            message = run.message,
            queuedAt = run.queuedAt,
            finishedAt = checkNotNull(run.finishedAt) { "run $runId is not finished" },
            sourceId = source.id,
            sourceName = source.name,
            agentId = agentId,
            agentHostname = agent.hostname,
            stepStatus = stepView.status,
            startedAt = run.startedAt,
            backup = stepView.backup?.let { BackupSizes(it.totalBytes, it.addedBytes) },
            previousStatus = previousStatus(session, run),
        )
    }

    private fun previousStatus(
        session: Session,
        run: RunRecord,
    ): RunState? =
        session
            .createSelectionQuery(PREVIOUS_STATUS, String::class.java)
            .setParameter("source", run.sourceId)
            .setParameter("queued", run.queuedAt)
            .setParameter("run", run.id)
            .setMaxResults(1)
            .uniqueResult()
            ?.let(RunState::of)
}
