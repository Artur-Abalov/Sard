// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.persistence.UuidV7
import org.hibernate.Session
import java.time.Duration
import java.time.Instant
import java.util.UUID

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). Each live source with verification on: its last pass,
// its first whole snapshot and its open episode.
private const val WATCHED = """
    select s.id,
        (select rv.id from restore_verifications rv
            where rv.tenant_id = s.tenant_id and rv.source_id = s.id and rv.status = 'passed'
            order by rv.verified_at desc, rv.id desc limit 1),
        (select max(rv.verified_at) from restore_verifications rv
            where rv.tenant_id = s.tenant_id and rv.source_id = s.id and rv.status = 'passed'),
        (select min(sn.created_at) from snapshots sn
            where sn.tenant_id = s.tenant_id and sn.source_id = s.id and not sn.partial),
        e.id
    from sources s
    left join verification_stale_episodes e on e.tenant_id = s.tenant_id and e.source_id = s.id and e.closed_at is null
    where s.tenant_id = :tenant and s.deleted_at is null and s.verification_enabled
    order by s.id"""

// Open episodes of sources deleted or no longer verified: they end without a pass.
private const val UNWATCHED = """
    select e.id, true from verification_stale_episodes e
    join sources s on s.tenant_id = e.tenant_id and s.id = e.source_id
    where e.tenant_id = :tenant and e.closed_at is null and (s.deleted_at is not null or not s.verification_enabled)"""
private const val OPEN = """
    insert into verification_stale_episodes (id, tenant_id, source_id, counted_from, opened_at)
    values (:id, :tenant, :source, :counted, :now) on conflict do nothing"""
private const val CLOSE_EPISODE =
    "update verification_stale_episodes set closed_at = :now, closed_by_id = :pass " +
        "where tenant_id = :tenant and id = :id and closed_at is null"

/** What the server knows of a source for [Staleness]. */
data class StaleFacts(
    val lastPass: Instant?,
    val lastPassId: UUID?,
    /** When its first whole snapshot was made; null without one. */
    val firstSnapshot: Instant?,
    val openEpisode: UUID?,
)

/** What a tick does with a source's episode. */
sealed interface StaleTurn {
    data object Keep : StaleTurn

    data class Open(
        val countedFrom: Instant,
    ) : StaleTurn

    data class Close(
        val episode: UUID,
        val closedBy: UUID?,
    ) : StaleTurn
}

/**
 * «Not verified successfully for longer than N days» (V4, for V6): the design document's most important alert,
 * since a silent or broken verifier must not look like success. Counted from the last pass, or from the first whole
 * snapshot of a source never passed; a source without one has nothing to verify. One event per turn: an episode
 * opens once and the next pass closes it.
 */
object Staleness {
    fun judge(
        facts: StaleFacts,
        now: Instant,
        after: Duration,
    ): StaleTurn {
        val countedFrom = facts.lastPass ?: facts.firstSnapshot ?: return StaleTurn.Keep
        val stale = Duration.between(countedFrom, now) > after
        return if (stale) open(facts, countedFrom) else close(facts)
    }

    /** Stale: an episode opens unless one is open. */
    private fun open(
        facts: StaleFacts,
        countedFrom: Instant,
    ): StaleTurn = if (facts.openEpisode == null) StaleTurn.Open(countedFrom) else StaleTurn.Keep

    /** Fresh: an open episode closes with the pass that made it fresh. */
    private fun close(facts: StaleFacts): StaleTurn {
        val episode = facts.openEpisode ?: return StaleTurn.Keep
        return StaleTurn.Close(episode, facts.lastPassId)
    }
}

/** Opens and closes the stale episodes of a tenant once per tick, guarded so a second server changes nothing. */
class StaleEpisodes(
    private val ids: UuidV7,
    private val after: Duration,
) {
    fun update(
        session: Session,
        tenantId: UUID,
        now: Instant,
    ) {
        for (row in rows(session, WATCHED, tenantId)) {
            val facts = StaleFacts(row[2] as Instant?, row[1] as UUID?, row[3] as Instant?, row[4] as UUID?)
            when (val turn = Staleness.judge(facts, now, after)) {
                StaleTurn.Keep -> Unit
                is StaleTurn.Open -> open(session, tenantId, row[0] as UUID, turn.countedFrom, now)
                is StaleTurn.Close -> close(session, tenantId, turn, now)
            }
        }
        for (row in rows(session, UNWATCHED, tenantId)) {
            close(session, tenantId, StaleTurn.Close(row[0] as UUID, null), now)
        }
    }

    private fun rows(
        session: Session,
        sql: String,
        tenantId: UUID,
    ): List<Array<Any?>> =
        session
            .createNativeQuery(sql.trimIndent(), Array<Any?>::class.java)
            .setParameter("tenant", tenantId)
            .list()

    private fun open(
        session: Session,
        tenantId: UUID,
        sourceId: UUID,
        countedFrom: Instant,
        now: Instant,
    ) = session
        .createNativeMutationQuery(OPEN.trimIndent())
        .setParameter("id", ids.next())
        .setParameter("tenant", tenantId)
        .setParameter("source", sourceId)
        .setParameter("counted", countedFrom)
        .setParameter("now", now)
        .executeUpdate()

    private fun close(
        session: Session,
        tenantId: UUID,
        turn: StaleTurn.Close,
        now: Instant,
    ) = session
        .createNativeMutationQuery(CLOSE_EPISODE)
        .setParameter("now", now)
        .setParameter("pass", turn.closedBy, UUID::class.java)
        .setParameter("tenant", tenantId)
        .setParameter("id", turn.episode)
        .executeUpdate()
}
