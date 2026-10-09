// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import dev.sard.server.runs.RunsTenant
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID

/** Spring properties of scheduler tests: the background loop never ticks during a test. */
internal const val QUIET_LOOP = "sard.scheduler.interval=1h"
internal const val SPACING = "sard.scheduler.catch-up-spacing=10s"

/** One fire as the journal stores it. */
internal data class FireRow(
    val kind: String,
    val scheduledFor: Instant,
    val outcome: String,
    val runId: UUID?,
    val reason: String?,
    val missedCount: Int?,
    val skippedInRow: Int,
    val alert: Boolean,
    val missedCountCapped: Boolean = false,
)

/** One run as the scheduler left it. */
internal data class RunRow(
    val id: UUID,
    val sourceId: UUID,
    val trigger: String,
    val scheduleId: UUID?,
)

/** Reads and cleans what the scheduler writes for a [RunsTenant]. */
internal class ScheduleTables(
    private val jdbc: JdbcTemplate,
    private val tenant: RunsTenant,
) {
    fun runs(): List<RunRow> =
        jdbc.query(
            "select id, source_id, trigger, schedule_id from runs where tenant_id = ? order by queued_at, id",
            { rs, _ ->
                RunRow(
                    rs.getObject(1, UUID::class.java),
                    rs.getObject(2, UUID::class.java),
                    rs.getString(3),
                    rs.getObject(4, UUID::class.java),
                )
            },
            tenant.id,
        )

    fun fires(scheduleId: UUID): List<FireRow> =
        jdbc.query(
            """
            select kind, scheduled_for, outcome, run_id, reason, missed_count, skipped_in_row, alert, missed_count_capped
            from schedule_fires where tenant_id = ? and schedule_id = ? order by recorded_at, scheduled_for, kind
            """.trimIndent(),
            { rs, _ ->
                FireRow(
                    rs.getString(1),
                    rs.getTimestamp(2).toInstant(),
                    rs.getString(3),
                    rs.getObject(4, UUID::class.java),
                    rs.getString(5),
                    rs.getObject(6) as Int?,
                    rs.getInt(7),
                    rs.getBoolean(8),
                    rs.getBoolean(9),
                )
            },
            tenant.id,
            scheduleId,
        )

    /** Stands in for the agent: every active run of the tenant succeeds. */
    fun finishAll(at: Instant) {
        val time = Timestamp.from(at)
        jdbc.update(
            """
            update run_steps set status = 'succeeded', dispatched_at = ?, finished_at = ?
            where tenant_id = ? and status in ('queued', 'dispatched', 'running')
            """.trimIndent(),
            time,
            time,
            tenant.id,
        )
        jdbc.update(
            "update runs set status = 'succeeded', finished_at = ? where tenant_id = ? and finished_at is null",
            time,
            tenant.id,
        )
    }

    fun drop() {
        jdbc.update("delete from schedule_fires where tenant_id = ?", tenant.id)
        jdbc.update("update runs set schedule_id = null where tenant_id = ?", tenant.id)
        jdbc.update("delete from schedules where tenant_id = ?", tenant.id)
        tenant.drop()
    }
}

/** Records what the scheduler reports instead of registering meters. */
internal class RecordingSchedulerMetrics : SchedulerMetrics {
    val fired = java.util.concurrent.CopyOnWriteArrayList<Pair<FireKind, FireOutcome>>()
    val lags = java.util.concurrent.CopyOnWriteArrayList<java.time.Duration>()

    override fun fired(
        kind: FireKind,
        outcome: FireOutcome,
    ) {
        fired += kind to outcome
    }

    override fun lag(behind: java.time.Duration) {
        lags += behind
    }
}
