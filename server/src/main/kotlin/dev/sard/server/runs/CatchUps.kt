// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.RunRecord
import dev.sard.server.persistence.ScheduleRecord
import org.hibernate.Session
import java.time.Instant
import java.util.UUID

private const val CAPPED = 1

// The downtime rows that the catch-up fire of the run claimed (migration V202610101200).
private const val PERIOD =
    "select min(d.scheduledFor), max(d.missedUntil), sum(d.missedCount), " +
        "max(case when d.missedCountCapped then 1 else 0 end) " +
        "from ScheduleFireRecord d where d.catchUpFireId = " +
        "(select f.id from ScheduleFireRecord f where f.runId = :run and f.kind = 'catch_up')"

/** The period a catch-up run stands for, read from the journal of its schedule (F3b, K5). */
internal object CatchUps {
    /** Null unless [run] is a catch-up run with downtimes behind it. */
    fun of(
        session: Session,
        run: RunRecord,
    ): CatchUpPeriod? {
        val scheduleId = run.scheduleId
        if (scheduleId == null || Trigger.of(run.trigger) != Trigger.CATCH_UP) return null
        return periodOf(session, run.id, scheduleId)
    }

    private fun periodOf(
        session: Session,
        runId: UUID,
        scheduleId: UUID,
    ): CatchUpPeriod? {
        val row = session.createSelectionQuery(PERIOD, Array<Any?>::class.java).setParameter("run", runId).singleResult
        val from = row[0] as Instant? ?: return null
        val zone = session.find(ScheduleRecord::class.java, scheduleId).timezone
        val capped = (row[3] as Number).toInt() == CAPPED
        return CatchUpPeriod(from, row[1] as Instant, (row[2] as Number).toInt(), capped, zone)
    }
}
