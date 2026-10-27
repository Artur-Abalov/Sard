// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.fleet.CertificateEvent
import dev.sard.server.fleet.CurrentCertificate
import dev.sard.server.fleet.currentCertificates
import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentCertificateEventRecord
import dev.sard.server.persistence.OverdueEpisodeRecord
import dev.sard.server.persistence.RunRecord
import dev.sard.server.persistence.RunStepRecord
import dev.sard.server.persistence.ScheduleFireRecord
import dev.sard.server.persistence.ScheduleRecord
import dev.sard.server.persistence.SourceRecord
import dev.sard.server.runs.RunKind
import dev.sard.server.runs.RunState
import dev.sard.server.runs.RunViews
import dev.sard.server.runs.StepView
import dev.sard.server.runs.Trigger
import dev.sard.server.scheduler.FireOutcome
import dev.sard.server.scheduler.FireReason
import org.hibernate.Session
import org.hibernate.query.SelectionQuery
import java.time.Instant
import java.util.UUID

private const val FIRST_STEP = "from RunStepRecord s where s.runId = :run order by s.ordinal"

// D6 keeps one active run per source, so every run queued before this one has finished: the answer is fixed.
private const val PREVIOUS_STATUS =
    "select r.status from RunRecord r where r.sourceId = :source and r.kind = 'backup' and r.finishedAt is not null " +
        "and r.trigger <> 'verification' " +
        "and (r.queuedAt < :queued or (r.queuedAt = :queued and r.id < :run)) order by r.queuedAt desc, r.id desc"

/** What a claimed delivery tells about, read back from the database at send time (S9a, F3b). */
internal object Notices {
    /** An overdue episode (F6); for its closing, with the run that closed it. */
    fun overdue(
        session: Session,
        tenantId: UUID,
        delivery: DueDelivery,
    ): OverdueNotice {
        val episode = session.find(OverdueEpisodeRecord::class.java, checkNotNull(delivery.episodeId))
        val source = session.find(SourceRecord::class.java, episode.sourceId)
        val closing = episode.closedByRunId?.takeIf { delivery.episodeEvent == EPISODE_CLOSED }
        return OverdueNotice(
            tenantId = tenantId,
            episodeId = episode.id,
            sourceId = source.id,
            sourceName = source.name,
            agentHostname = session.find(Agent::class.java, source.agentId).hostname,
            countedFrom = episode.countedFrom,
            deadline = episode.deadline,
            closedBy = closing?.let { finished(session, tenantId, it) },
        )
    }

    /**
     * The certificate of a delivery's event, or null when there is nothing to tell any more (П37): the certificate is
     * not the agent's current one (superseded by a confirmed renewal, revoked, or its agent revoked), or the event
     * is a threshold and the certificate has expired since: the expiry notice tells of the end.
     */
    fun certificate(
        session: Session,
        delivery: DueDelivery,
        now: Instant,
        connected: AgentConnected,
    ): CertificateNotice? {
        val event = session.find(AgentCertificateEventRecord::class.java, checkNotNull(delivery.certificateEventId))
        val current = currentCertificates(session, listOf(event.agentId))[event.agentId]
        val kind = CertificateEvent.of(event.event)
        if (current == null || current.serial != event.serial || endedBefore(kind, current, now)) return null
        val agent = session.find(Agent::class.java, event.agentId)
        return CertificateNotice(
            tenantId = delivery.tenantId,
            agentId = agent.id,
            hostname = agent.hostname,
            builtin = agent.builtin,
            event = kind,
            notAfter = current.lifetime.notAfter,
            now = now,
            renewalIssuedAt = current.renewalIssuedAt,
            online = connected.online(agent.id),
            lastSeenAt = agent.lastSeenAt,
        )
    }

    /** A threshold event of a certificate that has expired by [now]: the expiry notice tells of the end. */
    private fun endedBefore(
        kind: CertificateEvent,
        current: CurrentCertificate,
        now: Instant,
    ) = kind != CertificateEvent.EXPIRED && !now.isBefore(current.lifetime.notAfter)

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
            kind = RunKind.of(run.kind),
            cancelRequested = run.cancelRequestedAt != null,
            timeoutSeconds = stepView.timeoutSeconds,
            agentUnavailableSince = run.agentUnavailableSince,
            restore = restoreNotice(run, stepView),
        )
    }

    /** What a restore run restored; null for a backup. */
    private fun restoreNotice(
        run: RunRecord,
        step: StepView,
    ): RestoreNotice? {
        val info = run.restore?.let(RunViews::restoreInfo) ?: return null
        return RestoreNotice(info.snapshotId, info.snapshotCreatedAt, step.restore)
    }

    private fun notifiesOnSuccess(
        session: Session,
        run: RunRecord,
    ): Boolean = run.scheduleId?.let { session.find(ScheduleRecord::class.java, it).notifyOnSuccess } ?: false

    /**
     * How many runs failed in a row right before [run], of any trigger; a cancelled or succeeded one ends it. A run
     * that was not run because its agent was unavailable is neither a failure nor the end of the series (J1c, Р-J14).
     */
    private fun failuresBefore(
        session: Session,
        run: RunRecord,
    ): Int =
        previousStatuses(session, run).resultStream.use { statuses ->
            statuses
                .map(RunState::of)
                .filter { it != RunState.AGENT_UNAVAILABLE }
                .takeWhile { it == RunState.FAILED }
                .count()
                .toInt()
        }

    private fun previousStatus(
        session: Session,
        run: RunRecord,
    ): RunState? =
        previousStatuses(session, run)
            .setMaxResults(1)
            .uniqueResult()
            ?.let(RunState::of)

    /** The statuses of the finished runs of the source before [run], nearest first. */
    private fun previousStatuses(
        session: Session,
        run: RunRecord,
    ): SelectionQuery<String> =
        session
            .createSelectionQuery(PREVIOUS_STATUS, String::class.java)
            .setParameter("source", run.sourceId)
            .setParameter("queued", run.queuedAt)
            .setParameter("run", run.id)
}
