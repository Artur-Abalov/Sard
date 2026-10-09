// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.RunRecord
import dev.sard.server.persistence.RunStepRecord
import dev.sard.server.persistence.ScheduleFireRecord
import dev.sard.server.persistence.ScheduleRecord
import dev.sard.server.persistence.SourceRecord
import dev.sard.server.runs.RunState
import dev.sard.server.runs.RunViews
import dev.sard.server.runs.Trigger
import dev.sard.server.scheduler.FireOutcome
import dev.sard.server.scheduler.FireReason
import org.hibernate.Session
import java.util.UUID

private const val FIRST_STEP = "from RunStepRecord s where s.runId = :run order by s.ordinal"

// D6 keeps one active run per source, so every run queued before this one has finished: the answer is fixed.
private const val PREVIOUS_STATUS =
    "select r.status from RunRecord r where r.sourceId = :source and r.finishedAt is not null " +
        "and (r.queuedAt < :queued or (r.queuedAt = :queued and r.id < :run)) order by r.queuedAt desc, r.id desc"

/** What a claimed delivery tells about, read back from the database at send time (S9a, F3b). */
internal object Notices {
    fun alert(
        session: Session,
        tenantId: UUID,
        fireId: UUID,
    ): SkipAlertNotice {
        val fire = session.find(ScheduleFireRecord::class.java, fireId)
        val schedule = session.find(ScheduleRecord::class.java, fire.scheduleId)
        val source = session.find(SourceRecord::class.java, schedule.sourceId)
        return SkipAlertNotice(
            tenantId = tenantId,
            fireId = fireId,
            sourceId = source.id,
            sourceName = source.name,
            agentHostname = session.find(Agent::class.java, source.agentId).hostname,
            skippedInRow = fire.skippedInRow,
            outcome = FireOutcome.of(fire.outcome),
            reason = fire.reason?.let(FireReason::of),
            scheduledFor = fire.scheduledFor,
            timezone = schedule.timezone,
            activeRunId = fire.runId.takeIf { FireOutcome.of(fire.outcome) == FireOutcome.SKIPPED_ACTIVE },
        )
    }

    fun finished(
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
            failuresBefore = failuresBefore(session, run),
            notifyOnSuccess = notifiesOnSuccess(session, run),
        )
    }

    private fun notifiesOnSuccess(
        session: Session,
        run: RunRecord,
    ): Boolean = run.scheduleId?.let { session.find(ScheduleRecord::class.java, it).notifyOnSuccess } ?: false

    /** How many runs failed in a row right before [run], of any trigger; a cancelled or succeeded one ends it. */
    private fun failuresBefore(
        session: Session,
        run: RunRecord,
    ): Int =
        session
            .createSelectionQuery(PREVIOUS_STATUS, String::class.java)
            .setParameter("source", run.sourceId)
            .setParameter("queued", run.queuedAt)
            .setParameter("run", run.id)
            .resultStream
            .use { statuses -> statuses.takeWhile { RunState.of(it) == RunState.FAILED }.count().toInt() }

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
