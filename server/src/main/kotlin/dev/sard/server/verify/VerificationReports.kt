// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.runs.SourceNotFound
import dev.sard.server.runs.VerificationRuns
import org.hibernate.Session
import java.time.Clock
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.util.UUID

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). A deleted source keeps its history.
private const val SOURCE = "select name, deleted_at is not null from sources where tenant_id = :tenant and id = :id"
private const val MARKS = """
    select status, verified_at from restore_verifications
    where tenant_id = :tenant and source_id = :source order by verified_at, id"""
private const val IN_PERIOD = """
    select rv.id, rv.verified_at, sn.snapshot_id, a.hostname, rv.status, rv.level, rv.restore_duration_ms,
        cast(rv.stages as text)
    from restore_verifications rv
    join snapshots sn on sn.tenant_id = rv.tenant_id and sn.id = rv.snapshot_id
    left join agents a on a.tenant_id = rv.tenant_id and a.id = rv.verifier_agent_id
    where rv.tenant_id = :tenant and rv.source_id = :source and rv.verified_at >= :from and rv.verified_at < :to
    order by rv.verified_at desc, rv.id desc"""

/** One verification of the report: what a reader of it needs, no evidence and no output. */
data class ReportEntry(
    val id: UUID,
    val verifiedAt: Instant,
    /** restic's id of the snapshot. */
    val snapshotId: String,
    val verifierHostname: String?,
    val status: VerificationStatus,
    val level: Int,
    val durationMillis: Long?,
    val restoreMillis: Long?,
)

/** The report of a source's verifications for a period (V6, rule 20). */
data class VerificationReport(
    val sourceId: UUID,
    val sourceName: String,
    val sourceDeleted: Boolean,
    val period: ReportPeriod,
    val timezone: String,
    val generatedAt: Instant,
    val items: List<ReportEntry>,
    val failures: List<FailureEpisode>,
) {
    val passed: Int get() = items.count { it.status == VerificationStatus.PASSED }
    val failed: Int get() = items.count { it.status == VerificationStatus.FAILED }
    val notHeld: Int get() = items.size - passed - failed
}

/** Reads the report of a source in a tenant; one transaction. */
class VerificationReports(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val schedule: VerificationSchedule,
) {
    /** Throws [SourceNotFound] for no such source in the tenant and [InvalidReportPeriod] for an impossible period. */
    fun of(
        tenantId: UUID,
        sourceId: UUID,
        from: LocalDate?,
        to: LocalDate?,
    ): VerificationReport =
        sessions.inTenant(tenantId) { session ->
            val source = source(session, tenantId, sourceId)
            val timezone = schedule.current(session).timezone
            val now = clock.instant()
            val period = ReportPeriod.resolve(from, to, ZoneId.of(timezone), now)
            VerificationReport(
                sourceId = sourceId,
                sourceName = source.first,
                sourceDeleted = source.second,
                period = period,
                timezone = timezone,
                generatedAt = now,
                items = items(session, tenantId, sourceId, period),
                failures = FailureEpisodes.of(marks(session, tenantId, sourceId)).filter { it.overlaps(period) },
            )
        }

    private fun source(
        session: Session,
        tenantId: UUID,
        sourceId: UUID,
    ): Pair<String, Boolean> =
        session
            .createNativeQuery(SOURCE, Array<Any?>::class.java)
            .setParameter("tenant", tenantId)
            .setParameter("id", sourceId)
            .list()
            .firstOrNull()
            ?.let { it[0] as String to it[1] as Boolean } ?: throw SourceNotFound(sourceId)

    private fun marks(
        session: Session,
        tenantId: UUID,
        sourceId: UUID,
    ): List<HistoryMark> =
        session
            .createNativeQuery(MARKS.trimIndent(), Array<Any?>::class.java)
            .setParameter("tenant", tenantId)
            .setParameter("source", sourceId)
            .list()
            .map { HistoryMark(VerificationStatus.of(it[0] as String), it[1] as Instant) }

    private fun items(
        session: Session,
        tenantId: UUID,
        sourceId: UUID,
        period: ReportPeriod,
    ): List<ReportEntry> =
        session
            .createNativeQuery(IN_PERIOD.trimIndent(), Array<Any?>::class.java)
            .setParameter("tenant", tenantId)
            .setParameter("source", sourceId)
            .setParameter("from", period.from)
            .setParameter("to", period.to)
            .list()
            .map { row ->
                ReportEntry(
                    id = row[ReportColumn.ID] as UUID,
                    verifiedAt = row[ReportColumn.AT] as Instant,
                    snapshotId = row[ReportColumn.SNAPSHOT] as String,
                    verifierHostname = row[ReportColumn.HOSTNAME] as String?,
                    status = VerificationStatus.of(row[ReportColumn.STATUS] as String),
                    level = (row[ReportColumn.LEVEL] as Number).toInt(),
                    durationMillis = ReportDuration.millis(stagesOf(row)),
                    restoreMillis = (row[ReportColumn.RESTORE] as Number?)?.toLong(),
                )
            }
}

/** The columns of IN_PERIOD, in its select order. */
private enum class ReportColumn {
    ID,
    AT,
    SNAPSHOT,
    HOSTNAME,
    STATUS,
    LEVEL,
    RESTORE,
    STAGES,
}

private operator fun Array<Any?>.get(column: ReportColumn): Any? = this[column.ordinal]

private fun stagesOf(row: Array<Any?>) = VerificationRuns.stagesOf(row[ReportColumn.STAGES] as String)
