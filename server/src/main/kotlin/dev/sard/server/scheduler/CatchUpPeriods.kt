// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import dev.sard.server.persistence.TenantSessions
import java.time.Instant
import java.util.UUID

private const val CAPPED = 1

// The downtime rows that the catch-up fire which created a run claimed (migration V202610101200). Only a fire that
// created its run counts: a later catch-up skipped on the same, still active run (run_id = that run) claims for itself.
private const val PERIODS =
    "select f.runId, min(d.scheduledFor), max(d.missedUntil), sum(d.missedCount), " +
        "max(case when d.missedCountCapped then 1 else 0 end), s.timezone " +
        "from ScheduleFireRecord f, ScheduleFireRecord d, ScheduleRecord s " +
        "where f.runId in :runs and f.kind = :kind and f.outcome = :outcome " +
        "and d.catchUpFireId = f.id and s.id = f.scheduleId " +
        "group by f.runId, s.timezone"

/** The periods that catch-up runs stand for, read from the journal of their schedules (F3b, K5). */
class CatchUpPeriods(
    private val sessions: TenantSessions,
) {
    /** The period of each of [runIds] that is a catch-up run with downtimes behind it; the others are absent. */
    fun periods(
        tenantId: UUID,
        runIds: Collection<UUID>,
    ): Map<UUID, CatchUpPeriod> {
        if (runIds.isEmpty()) return emptyMap()
        return sessions.inTenant(tenantId) { session ->
            session
                .createSelectionQuery(PERIODS, Array<Any?>::class.java)
                .setParameter("runs", runIds)
                .setParameter("kind", FireKind.CATCH_UP.stored)
                .setParameter("outcome", FireOutcome.RUN_CREATED.stored)
                .list()
                .associate { it[0] as UUID to period(it) }
        }
    }

    private fun period(row: Array<Any?>) =
        CatchUpPeriod(
            row[1] as Instant,
            row[2] as Instant,
            (row[3] as Number).toInt(),
            (row[4] as Number).toInt() == CAPPED,
            row[5] as String,
        )
}
