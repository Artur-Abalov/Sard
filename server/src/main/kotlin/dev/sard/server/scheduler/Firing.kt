// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import dev.sard.server.persistence.ScheduleFireRecord
import dev.sard.server.persistence.ScheduleRecord
import dev.sard.server.persistence.UuidV7
import dev.sard.server.runs.AgentRevoked
import dev.sard.server.runs.RunActive
import dev.sard.server.runs.RunView
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsException
import dev.sard.server.runs.SourceNotFound
import dev.sard.server.runs.UnknownPlugin
import dev.sard.server.runs.UnknownRepository
import org.hibernate.Session
import java.time.Instant
import java.util.UUID
import kotlin.reflect.KClass

// Native SQL names tenant_id explicitly (ADR 0013, rule 8): the tenant of the schedule being fired.
private const val CLAIM =
    "update schedule_fires set catch_up_fire_id = :fire where tenant_id = :tenant and schedule_id = :schedule " +
        "and outcome = 'skipped_downtime' and catch_up_fire_id is null and recorded_at >= :since"

/** A refused start of a schedule's run and what the journal says of it; [RunActive] is answered apart. */
private val REFUSALS: Map<KClass<out RunsException>, Pair<FireOutcome, FireReason>> =
    mapOf(
        SourceNotFound::class to (FireOutcome.SKIPPED_GONE to FireReason.SOURCE_DELETED),
        AgentRevoked::class to (FireOutcome.SKIPPED_GONE to FireReason.AGENT_REVOKED),
        UnknownPlugin::class to (FireOutcome.REFUSED to FireReason.UNKNOWN_PLUGIN),
        UnknownRepository::class to (FireOutcome.REFUSED to FireReason.UNKNOWN_REPOSITORY),
    )

/** What one locked schedule did: the runs it created and the journal rows it wrote, as (kind, outcome). */
internal class Fired(
    val runs: List<RunView>,
    val journal: List<Pair<FireKind, FireOutcome>>,
) {
    companion object {
        val NOTHING = Fired(emptyList(), emptyList())
    }
}

/** One locked [schedule] at [now], inside its transaction: what [Scheduler] does with a due schedule. */
internal class Firing(
    private val session: Session,
    private val schedule: ScheduleRecord,
    private val now: Instant,
    private val runs: Runs,
    private val ids: UuidV7,
    private val settings: SchedulerSettings,
) {
    private val journal = mutableListOf<Pair<FireKind, FireOutcome>>()

    /** The catch-up first if its slot came, then the cron. */
    fun fire(slots: CatchUpSlots): Fired {
        val runs = listOfNotNull(catchUp(), cron(slots))
        return Fired(runs, journal.toList())
    }

    /** A schedule is locked when either is due: the catch-up waits if only the cron is. */
    private fun catchUp(): RunView? {
        val at = schedule.catchUpAt?.takeIf { it <= now } ?: return null
        val since = schedule.catchUpOwedSince ?: Instant.EPOCH
        schedule.catchUpAt = null
        schedule.catchUpOwedSince = null
        return attempt(at, FireKind.CATCH_UP, since)
    }

    private fun cron(slots: CatchUpSlots): RunView? {
        val next = schedule.nextRunAt ?: return null
        return onCron(Due.of(CronSchedule.parse(schedule.cron, schedule.timezone), next, now), slots)
    }

    private fun onCron(
        due: Due,
        slots: CatchUpSlots,
    ): RunView? =
        when (due) {
            Due.NotYet -> null
            is Due.OnTime -> attempt(due.at, FireKind.SCHEDULE).also { schedule.nextRunAt = due.next }
            is Due.Missed -> downtime(due, slots)
        }

    /** One row for the whole downtime; a catch-up is owed, at the earlier slot if one is owed already. */
    private fun downtime(
        due: Due.Missed,
        slots: CatchUpSlots,
    ): RunView? {
        schedule.nextRunAt = due.next
        if (schedule.catchUpAt == null) {
            schedule.catchUpAt = slots.take()
            schedule.catchUpOwedSince = now
        }
        journal(FireKind.SCHEDULE, due.first, Result(FireOutcome.SKIPPED_DOWNTIME), Downtime(due))
        return null
    }

    private fun attempt(
        at: Instant,
        kind: FireKind,
        owedSince: Instant = Instant.EPOCH,
    ): RunView? {
        val result =
            try {
                val run = runs.startScheduled(session, schedule.sourceId, kind.trigger, schedule.id)
                Result(FireOutcome.RUN_CREATED, run, run.id)
            } catch (e: RunActive) {
                Result(FireOutcome.SKIPPED_ACTIVE, runId = e.activeRunId)
            } catch (e: RunsException) {
                refusal(e)
            }
        schedule.skippedInRow = if (result.outcome.skip) schedule.skippedInRow + 1 else 0
        schedule.lastFiredAt = now
        journal(kind, at, result, owedSince = owedSince)
        return result.run
    }

    private fun journal(
        kind: FireKind,
        at: Instant,
        result: Result,
        downtime: Downtime? = null,
        owedSince: Instant = Instant.EPOCH,
    ) {
        val record =
            ScheduleFireRecord(
                id = ids.next(),
                scheduleId = schedule.id,
                kind = kind.stored,
                scheduledFor = at,
                outcome = result.outcome.stored,
                runId = result.runId,
                reason = result.reason,
                missedCount = downtime?.count,
                missedUntil = downtime?.until,
                missedCountCapped = downtime?.capped ?: false,
                skippedInRow = schedule.skippedInRow,
                alert = alerts(result),
                recordedAt = now,
            )
        session.persist(record)
        if (kind == FireKind.CATCH_UP) claimDowntimes(record.id, owedSince)
        journal += kind to result.outcome
    }

    /** The catch-up fire stands for the downtimes journaled since it became owed; earlier ones were cancelled (Р15). */
    private fun claimDowntimes(
        fire: UUID,
        since: Instant,
    ) {
        session.flush()
        session
            .createNativeMutationQuery(CLAIM)
            .setParameter("fire", fire)
            .setParameter("since", since)
            .setParameter("tenant", schedule.tenantId)
            .setParameter("schedule", schedule.id)
            .executeUpdate()
    }

    /** The skip that brings the series to the threshold raises the alert, once. */
    private fun alerts(result: Result) = result.outcome.skip && schedule.skippedInRow == settings.skipAlertThreshold
}

private fun refusal(e: RunsException): Result {
    val (outcome, reason) = REFUSALS[e::class] ?: throw e
    return Result(outcome, reason = reason.stored)
}

/** A downtime row: [count] fires passed, the last at [until]; [capped] when more passed than were counted. */
private class Downtime(
    val count: Int,
    val until: Instant,
    val capped: Boolean,
) {
    constructor(due: Due.Missed) : this(due.count, due.last, due.capped)
}

/** What came of a fire: the run created, or the active run that skipped it ([runId]), or the stored reason. */
private class Result(
    val outcome: FireOutcome,
    val run: RunView? = null,
    val runId: UUID? = null,
    val reason: String? = null,
)
