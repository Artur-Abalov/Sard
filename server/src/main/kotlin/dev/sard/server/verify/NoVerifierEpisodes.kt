// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.persistence.UuidV7
import org.hibernate.Session
import java.time.Instant
import java.util.UUID

// Native SQL names tenant_id explicitly (ADR 0013, rule 8).
private const val SOURCES_TO_VERIFY =
    "select count(*) from sources where tenant_id = :tenant and deleted_at is null and verification_enabled"
private const val FIND_OPEN_EPISODE =
    "select id from no_verifier_episodes where tenant_id = :tenant and closed_at is null"
private const val OPEN_EPISODE_ROW =
    "insert into no_verifier_episodes (id, tenant_id, sources, opened_at) values (:id, :tenant, :sources, :now) " +
        "on conflict do nothing"
private const val CLOSE_EPISODE_ROW =
    "update no_verifier_episodes set closed_at = :now where tenant_id = :tenant and id = :id and closed_at is null"

/** What a tick does with the «no verifier» episode of a tenant. */
sealed interface NoVerifierTurn {
    data object Keep : NoVerifierTurn

    data class Open(
        val sources: Int,
    ) : NoVerifierTurn

    data class Close(
        val episode: UUID,
    ) : NoVerifierTurn
}

/**
 * The «no verifier» state of an installation (V6, ОВ4): sources with verification on and not one verifier that is not
 * revoked. It is a state, not an event: an episode opens when it begins and closes when it ends, and only the opening
 * is told.
 */
object NoVerifierState {
    fun judge(
        sources: Int,
        anyVerifier: Boolean,
        open: UUID?,
    ): NoVerifierTurn {
        val inState = sources > 0 && !anyVerifier
        return if (inState) opening(sources, open) else closing(open)
    }

    private fun opening(
        sources: Int,
        open: UUID?,
    ): NoVerifierTurn = if (open == null) NoVerifierTurn.Open(sources) else NoVerifierTurn.Keep

    private fun closing(open: UUID?): NoVerifierTurn = open?.let { NoVerifierTurn.Close(it) } ?: NoVerifierTurn.Keep
}

/** Opens and closes the «no verifier» episode of a tenant once per tick, guarded so a second server changes nothing. */
class NoVerifierEpisodes(
    private val ids: UuidV7,
    private val pool: VerifierPool,
) {
    fun update(
        session: Session,
        tenantId: UUID,
        now: Instant,
    ) {
        val sources = (scalar(session, SOURCES_TO_VERIFY, tenantId) as Number).toInt()
        val open = scalar(session, FIND_OPEN_EPISODE, tenantId) as UUID?
        when (val turn = NoVerifierState.judge(sources, pool.any(session, tenantId), open)) {
            NoVerifierTurn.Keep -> Unit
            is NoVerifierTurn.Open -> open(session, tenantId, turn.sources, now)
            is NoVerifierTurn.Close -> close(session, tenantId, turn.episode, now)
        }
    }

    private fun scalar(
        session: Session,
        sql: String,
        tenantId: UUID,
    ): Any? =
        session
            .createNativeQuery(sql, Any::class.java)
            .setParameter("tenant", tenantId)
            .list()
            .firstOrNull()

    private fun open(
        session: Session,
        tenantId: UUID,
        sources: Int,
        now: Instant,
    ) = session
        .createNativeMutationQuery(OPEN_EPISODE_ROW)
        .setParameter("id", ids.next())
        .setParameter("tenant", tenantId)
        .setParameter("sources", sources)
        .setParameter("now", now)
        .executeUpdate()

    private fun close(
        session: Session,
        tenantId: UUID,
        episode: UUID,
        now: Instant,
    ) = session
        .createNativeMutationQuery(CLOSE_EPISODE_ROW)
        .setParameter("tenant", tenantId)
        .setParameter("id", episode)
        .setParameter("now", now)
        .executeUpdate()
}
