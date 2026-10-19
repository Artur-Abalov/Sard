// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import org.hibernate.Session
import java.time.Instant
import java.util.UUID

// Native SQL names tenant_id explicitly (ADR 0013, rule 8).
private const val VERIFIERS =
    "select id, hostname, verifier_reported, machine_id, verify_free_bytes from agents " +
        "where tenant_id = :tenant and role = 'verifier' and revoked_at is null order by id"
private const val KEYS =
    "select agent_id, repository_id, name from agent_repositories " +
        "where tenant_id = :tenant and repository_id is not null order by agent_id, name"
private const val PLUGINS =
    "select agent_id, name from agent_plugins where tenant_id = :tenant and 'verify' = any(actions)"
private const val BUSY =
    "select distinct agent_id, true from run_steps where tenant_id = :tenant and action = 'verify' " +
        "and status in ('queued', 'dispatched', 'running')"
private const val RECENT =
    "select agent_id, count(*) from run_steps where tenant_id = :tenant and action = 'verify' " +
        "and queued_at >= :since group by agent_id"

/** Whether an agent holds a session now; over the stream manager in production. */
fun interface OnlineAgents {
    fun online(agentId: UUID): Boolean
}

/**
 * The live verifiers of a tenant as V4 «Выдача» judges them (V1 role, not revoked): what their last Register
 * reported, whether they are online, busy, and how many verifications they got lately.
 */
class VerifierPool(
    private val online: OnlineAgents,
) {
    fun candidates(
        session: Session,
        tenantId: UUID,
        since: Instant,
    ): List<Candidate> {
        val keys = rows(session, KEYS, tenantId).groupBy({ it[0] as UUID }, { it[1] as String to it[2] as String })
        val plugins = rows(session, PLUGINS, tenantId).groupBy({ it[0] as UUID }, { it[1] as String })
        val busy = rows(session, BUSY, tenantId).map { it[0] as UUID }.toSet()
        val recent = rows(session, RECENT, tenantId, since).associate { it[0] as UUID to (it[1] as Number).toInt() }
        return rows(session, VERIFIERS, tenantId).map { row ->
            val id = row[0] as UUID
            Candidate(
                agentId = id,
                hostname = row[1] as String,
                online = online.online(id),
                reported = row[2] as Boolean,
                machineId = row[3] as String?,
                // Two names of one repository on a host: the first by name is used.
                repositories = keys[id].orEmpty().reversed().toMap(),
                verifyPlugins = plugins[id].orEmpty().toSet(),
                freeBytes = (row[4] as Number?)?.toLong(),
                busy = id in busy,
                recentVerifications = recent[id] ?: 0,
            )
        }
    }

    /** Whether the tenant has any live verifier: without one, no job is made (owner decision 7). */
    fun any(
        session: Session,
        tenantId: UUID,
    ): Boolean = rows(session, VERIFIERS, tenantId).isNotEmpty()

    private fun rows(
        session: Session,
        sql: String,
        tenantId: UUID,
        since: Instant? = null,
    ): List<Array<Any?>> {
        val query = session.createNativeQuery(sql, Array<Any?>::class.java).setParameter("tenant", tenantId)
        since?.let { query.setParameter("since", it) }
        return query.list()
    }
}
