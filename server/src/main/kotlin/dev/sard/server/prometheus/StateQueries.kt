// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.prometheus

import dev.sard.server.metrics.MetricRunKind
import dev.sard.server.metrics.SourceSuccess
import dev.sard.server.metrics.StateRows
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.role.AgentRole
import dev.sard.server.runs.RunState
import org.hibernate.Session
import java.time.Instant
import java.time.temporal.TemporalAccessor
import java.util.UUID

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). Each query reads every tenant, in the read-only system
// session, and there are as many queries as kinds of state, never as many as sources: a source is a row, not a query.

// Every live source and, in two scalar subqueries, its newest success of each kind, as Unix time; 0 where it has none.
// A backup is counted as FreshnessCheck and SelfBackupHealth count it: succeeded, kind backup, not a verification run.
private const val SOURCES = """
    select s.tenant_id, s.id,
           coalesce(extract(epoch from (
               select max(r.finished_at) from runs r
               where r.tenant_id = s.tenant_id and r.source_id = s.id and r.status = 'succeeded'
               and r.kind = 'backup' and r.trigger <> 'verification')), 0),
           coalesce(extract(epoch from (
               select max(v.verified_at) from restore_verifications v
               where v.tenant_id = s.tenant_id and v.source_id = s.id and v.status = 'passed')), 0)
    from sources s
    where s.deleted_at is null"""

// A revoked agent is not counted; the role is the agent's, fixed by its token.
private const val AGENTS = "select a.tenant_id, a.id, a.role from agents a where a.revoked_at is null"

private const val ACTIVE_RUNS = """
    select r.tenant_id, case when r.trigger = 'verification' then 'verify' else r.kind end, r.status, count(*)
    from runs r
    where r.status in ('queued', 'dispatched', 'running')
    group by r.tenant_id, 2, r.status"""

private const val JOBS = """
    select j.tenant_id, j.status, count(*)
    from verification_jobs j
    where j.status in ('queued', 'running')
    group by j.tenant_id, j.status"""

private const val UNDELIVERED = """
    select d.tenant_id, d.status, count(*)
    from notification_deliveries d
    where d.status in ('failed', 'expired') and d.created_at >= :since
    group by d.tenant_id, d.status"""

// The current certificate of an agent that is not revoked: not revoked itself and not superseded by a renewal, and
// whose predecessor, if any, is superseded (AgentCertificateCheck's SCAN says the same in HQL). One that has already
// expired is current as long as nobody revoked it.
private const val NEXT_EXPIRY = """
    select min(c.not_after)
    from agent_certificates c
    join agents a on a.tenant_id = c.tenant_id and a.id = c.agent_id
    left join agent_certificates p on p.tenant_id = c.tenant_id and p.serial = c.predecessor_serial
    where a.revoked_at is null and c.revoked_at is null and c.superseded_at is null
    and (c.predecessor_serial is null or p.superseded_at is not null)"""

/** The columns of a counted row, in the select order of [ACTIVE_RUNS], [JOBS] and [UNDELIVERED]. */
private fun Array<Any?>.count(): Long = (last() as Number).toLong()

/** The sums over tenants of rows `(tenant_id, key..., count)`, by what [key] makes of the row. */
private fun <K> List<Array<Any?>>.summed(key: (Array<Any?>) -> K): Map<K, Long> =
    groupBy(key).mapValues { (_, rows) -> rows.sumOf { it.count() } }

/**
 * What the state tick reads from the database (H1 phase 3, ADR 0013 system caller 12): six queries in one read-only
 * transaction across every tenant, whatever the number of sources, agents or runs. Rows come back as identifiers and
 * numbers; no name, path or text of a tenant is read.
 */
class StateQueries(
    private val sessions: TenantSessions,
) {
    fun read(undeliveredSince: Instant): StateRows = sessions.system { session -> rowsOf(session, undeliveredSince) }

    private fun rowsOf(
        session: Session,
        since: Instant,
    ): StateRows =
        StateRows(
            sources = rows(session, SOURCES).map(::sourceOf),
            agents = rows(session, AGENTS).map { AgentRole.of(it[2] as String) to it[1] as UUID },
            runsActive = rows(session, ACTIVE_RUNS).summed(::activeKey),
            verificationJobs = rows(session, JOBS).summed { it[1] as String },
            undelivered = rows(session, UNDELIVERED, since).summed { it[1] as String },
            nextCertificateExpiry =
                session
                    .createNativeQuery(NEXT_EXPIRY.trimIndent(), Any::class.java)
                    .singleResult
                    ?.let { Instant.from(it as TemporalAccessor) },
        )

    private fun rows(
        session: Session,
        sql: String,
        since: Instant? = null,
    ): List<Array<Any?>> {
        val query = session.createNativeQuery(sql.trimIndent(), Array<Any?>::class.java)
        if (since != null) query.setParameter("since", since)
        return query.list()
    }

    private fun activeKey(row: Array<Any?>) = MetricRunKind.ofTag(row[1] as String) to RunState.of(row[2] as String)

    private fun sourceOf(row: Array<Any?>): SourceSuccess =
        SourceSuccess(row[1] as UUID, unixOrNull(row[BACKUP_COLUMN]), unixOrNull(row[VERIFICATION_COLUMN]))

    /** A Unix time in seconds as the query returns it; 0 means none. */
    private fun unixOrNull(value: Any?): Instant? {
        val seconds = (value as Number).toDouble()
        return if (seconds == 0.0) null else Instant.ofEpochMilli(Math.round(seconds * MILLIS_PER_SECOND))
    }
}

private const val MILLIS_PER_SECOND = 1000.0

/** The columns of [SOURCES] after the tenant and the source. */
private const val BACKUP_COLUMN = 2
private const val VERIFICATION_COLUMN = 3
