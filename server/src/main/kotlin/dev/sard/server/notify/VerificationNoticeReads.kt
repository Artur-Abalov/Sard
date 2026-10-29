// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.runs.VerificationRuns
import dev.sard.server.verify.VerificationStatus
import org.hibernate.Session
import java.time.Instant
import java.util.UUID

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). A deleted source keeps its row and its name.
// previous: the record before this one that is a pass or a failure (ОВ11а); the last column: this pass closed an
// episode.
private const val VERIFICATION = """
    select s.name, rv.status, rv.level, rv.reason, sn.snapshot_id, a.hostname, cast(rv.stages as text),
        cast(rv.mismatches as text), rv.restore_duration_ms,
        (select p.status from restore_verifications p
            where p.tenant_id = rv.tenant_id and p.source_id = rv.source_id and p.status in ('failed', 'passed')
              and (p.verified_at, p.id) < (rv.verified_at, rv.id)
            order by p.verified_at desc, p.id desc limit 1),
        exists (select 1 from verification_stale_episodes e where e.tenant_id = rv.tenant_id and e.closed_by_id = rv.id)
    from restore_verifications rv
    join sources s on s.tenant_id = rv.tenant_id and s.id = rv.source_id
    join snapshots sn on sn.tenant_id = rv.tenant_id and sn.id = rv.snapshot_id
    left join agents a on a.tenant_id = rv.tenant_id and a.id = rv.verifier_agent_id
    where rv.tenant_id = :tenant and rv.id = :id"""

// The last pass and the last record of the source up to the moment the episode opened; a cancelled record is not an
// outcome (J1f).
private const val STALE = """
    select e.source_id, s.name, e.threshold_days, e.timezone,
        (select max(rv.verified_at) from restore_verifications rv
            where rv.tenant_id = e.tenant_id and rv.source_id = e.source_id and rv.status = 'passed'
              and rv.verified_at <= e.opened_at),
        (select rv.status from restore_verifications rv
            where rv.tenant_id = e.tenant_id and rv.source_id = e.source_id and rv.verified_at <= e.opened_at
              and rv.status <> 'cancelled'
            order by rv.verified_at desc, rv.id desc limit 1)
    from verification_stale_episodes e
    join sources s on s.tenant_id = e.tenant_id and s.id = e.source_id
    where e.tenant_id = :tenant and e.id = :id"""
private const val NO_VERIFIER =
    "select sources, true from no_verifier_episodes where tenant_id = :tenant and id = :id"

/** Reads back, at send time, what a delivery about restore verification tells (V6). */
internal object VerificationNoticeReads {
    fun verification(
        session: Session,
        tenantId: UUID,
        verificationId: UUID,
    ): VerificationNotice {
        val row = one(session, VERIFICATION, tenantId, verificationId)
        return VerificationNotice(
            tenantId = tenantId,
            verificationId = verificationId,
            sourceName = row[NoticeColumn.SOURCE] as String,
            status = VerificationStatus.of(row[NoticeColumn.STATUS] as String),
            level = (row[NoticeColumn.LEVEL] as Number).toInt(),
            reason = row[NoticeColumn.REASON] as String?,
            snapshotId = row[NoticeColumn.SNAPSHOT] as String,
            verifierHostname = row[NoticeColumn.HOSTNAME] as String?,
            stages = VerificationRuns.stagesOf(row[NoticeColumn.STAGES] as String),
            mismatches = VerificationRuns.mismatchesOf(row[NoticeColumn.MISMATCHES] as String),
            restoreMillis = (row[NoticeColumn.RESTORE] as Number?)?.toLong(),
            previous = (row[NoticeColumn.PREVIOUS] as String?)?.let(VerificationStatus::of),
            closedStaleEpisode = row[NoticeColumn.CLOSED_EPISODE] as Boolean,
        )
    }

    fun stale(
        session: Session,
        tenantId: UUID,
        episodeId: UUID,
    ): StaleNotice {
        val row = one(session, STALE, tenantId, episodeId)
        return StaleNotice(
            tenantId = tenantId,
            episodeId = episodeId,
            sourceId = row[0] as UUID,
            sourceName = row[1] as String,
            thresholdDays = (row[2] as Number).toInt(),
            timezone = row[3] as String,
            lastPassAt = row[4] as Instant?,
            lastOutcome = (row[5] as String?)?.let(VerificationStatus::of),
        )
    }

    fun noVerifier(
        session: Session,
        tenantId: UUID,
        episodeId: UUID,
    ) = NoVerifierNotice(tenantId, episodeId, (one(session, NO_VERIFIER, tenantId, episodeId)[0] as Number).toInt())

    private fun one(
        session: Session,
        sql: String,
        tenantId: UUID,
        id: UUID,
    ): Array<Any?> =
        session
            .createNativeQuery(sql.trimIndent(), Array<Any?>::class.java)
            .setParameter("tenant", tenantId)
            .setParameter("id", id)
            .singleResult
}

/** The columns of VERIFICATION, in its select order. */
private enum class NoticeColumn {
    SOURCE,
    STATUS,
    LEVEL,
    REASON,
    SNAPSHOT,
    HOSTNAME,
    STAGES,
    MISMATCHES,
    RESTORE,
    PREVIOUS,
    CLOSED_EPISODE,
}

private operator fun Array<Any?>.get(column: NoticeColumn): Any? = this[column.ordinal]
