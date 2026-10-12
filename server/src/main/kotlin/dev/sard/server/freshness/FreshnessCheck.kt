// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.freshness

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.scheduler.CronSchedule
import org.slf4j.LoggerFactory
import java.sql.Timestamp
import java.time.Clock
import java.time.Instant
import java.util.UUID

private val log = LoggerFactory.getLogger(FreshnessCheck::class.java)

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). Every live source with a schedule that is on, its
// newest success and its open episode.
private const val SCHEDULED = """
    select s.tenant_id, s.id, sc.cron, sc.timezone, sc.updated_at, ok.id, ok.finished_at, e.id, e.counted_from
    from sources s
    join schedules sc on sc.tenant_id = s.tenant_id and sc.source_id = s.id and sc.enabled
    left join lateral (
        select r.id, r.finished_at from runs r
        where r.tenant_id = s.tenant_id and r.source_id = s.id and r.status = 'succeeded'
        order by r.finished_at desc, r.id desc limit 1) ok on true
    left join overdue_episodes e on e.tenant_id = s.tenant_id and e.source_id = s.id and e.closed_at is null
    where s.deleted_at is null"""

// Open episodes of sources that are deleted or no longer scheduled: they end without a success.
private const val UNSCHEDULED = """
    select e.tenant_id, e.id from overdue_episodes e
    join sources s on s.tenant_id = e.tenant_id and s.id = e.source_id
    where e.closed_at is null and (s.deleted_at is not null or not exists (
        select 1 from schedules sc where sc.tenant_id = e.tenant_id and sc.source_id = e.source_id and sc.enabled))"""

private const val OPEN = """
    insert into overdue_episodes (id, tenant_id, source_id, counted_from, deadline, opened_at)
    values (:id, :tenant, :source, :counted, :deadline, :now)
    on conflict do nothing"""

private const val CLOSE = """
    update overdue_episodes set closed_at = :now, closed_by_run_id = :run
    where tenant_id = :tenant and id = :id and closed_at is null"""

/**
 * Opens and closes overdue episodes (F6, ADR 00XX-draft-source-freshness) once per tick: the scan reads across
 * tenants in the read-only system session (ADR 0013, caller 8); every write runs in the row's own tenant and is
 * guarded (an open episode per source at most, a close only of an open one), so a second server changes nothing.
 */
class FreshnessCheck(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val ids: UuidV7,
) {
    fun tick() {
        val now = clock.instant()
        val (scheduled, unscheduled) = sessions.system { session -> scan(session) }
        for (source in scheduled) {
            write(source.tenantId, Freshness.judge(source, now), source.sourceId, now)
        }
        for ((tenantId, episodeId) in unscheduled) {
            close(tenantId, episodeId, null, now)
        }
    }

    private fun scan(session: org.hibernate.Session): Pair<List<SourceFreshness>, List<Pair<UUID, UUID>>> {
        val scheduled =
            session
                .createNativeQuery(SCHEDULED.trimIndent(), Array<Any?>::class.java)
                .list()
                .mapNotNull(::freshnessOf)
        val unscheduled =
            session
                .createNativeQuery(UNSCHEDULED.trimIndent(), Array<Any?>::class.java)
                .list()
                .map { it[0] as UUID to it[1] as UUID }
        return scheduled to unscheduled
    }

    private fun write(
        tenantId: UUID,
        verdict: Verdict,
        sourceId: UUID,
        now: Instant,
    ) {
        when (verdict) {
            Verdict.Keep -> Unit
            is Verdict.Open -> open(tenantId, sourceId, verdict, now)
            is Verdict.Close -> close(tenantId, verdict.episodeId, verdict.runId, now)
        }
    }

    private fun open(
        tenantId: UUID,
        sourceId: UUID,
        verdict: Verdict.Open,
        now: Instant,
    ) = sessions.inTenant(tenantId) { session ->
        session
            .createNativeMutationQuery(OPEN)
            .setParameter("id", ids.next())
            .setParameter("tenant", tenantId)
            .setParameter("source", sourceId)
            .setParameter("counted", verdict.countedFrom, Instant::class.java)
            .setParameter("deadline", verdict.deadline)
            .setParameter("now", now)
            .executeUpdate()
    }

    private fun close(
        tenantId: UUID,
        episodeId: UUID,
        runId: UUID?,
        now: Instant,
    ) = sessions.inTenant(tenantId) { session ->
        session
            .createNativeMutationQuery(CLOSE)
            .setParameter("now", now)
            .setParameter("run", runId, UUID::class.java)
            .setParameter("tenant", tenantId)
            .setParameter("id", episodeId)
            .executeUpdate()
    }
}

/** One scanned row; null (and a warning) for a stored schedule that no longer parses. */
private fun freshnessOf(row: Array<Any?>): SourceFreshness? {
    val fires =
        runCatching { CronSchedule.parse(row[2] as String, row[3] as String) }
            .onFailure { log.warn("Source {}: its schedule does not parse, freshness not checked", row[1]) }
            .getOrNull() ?: return null
    return SourceFreshness(
        tenantId = row[0] as UUID,
        sourceId = row[1] as UUID,
        fires = fires,
        scheduleChangedAt = instant(row[4]),
        lastSuccess = (row[5] as UUID?)?.let { LastSuccess(it, instant(row[6])) },
        open = (row[7] as UUID?)?.let { OpenEpisode(it, row[8]?.let(::instant)) },
    )
}

private fun instant(value: Any?): Instant =
    when (value) {
        is Instant -> value
        is Timestamp -> value.toInstant()
        is java.time.OffsetDateTime -> value.toInstant()
        else -> error("not a time: $value")
    }
