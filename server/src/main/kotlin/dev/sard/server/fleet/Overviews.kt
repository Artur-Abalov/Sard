// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.persistence.TenantSessions
import org.hibernate.Session
import java.util.UUID

private const val LIVE_AGENTS = "select a.id, a.lastSeenAt from Agent a where a.revokedAt is null"
private const val ANY_TOKEN = "select count(t) from EnrollmentTokenRecord t where t.builtin = false"
private const val INITIALIZED =
    "select count(r) from AgentRepositoryRecord r where r.repositoryId is not null " +
        "and r.agentId in (select a.id from Agent a where a.revokedAt is null)"
private const val LIVE_SOURCES = "select count(s) from SourceRecord s where s.deletedAt is null"
private const val SUCCEEDED_RUNS = "select count(r) from RunRecord r where r.status = 'succeeded'"

/** The five first steps of a tenant, each by its own rule; computed on every request, never stored (W2 К14). */
data class FirstSteps(
    val tokenIssued: Boolean,
    val agentConnected: Boolean,
    val repositoryInitialized: Boolean,
    val sourceCreated: Boolean,
    val backupSucceeded: Boolean,
) {
    val complete: Boolean
        get() = listOf(tokenIssued, agentConnected, repositoryInitialized, sourceCreated, backupSucceeded).all { it }
}

/** What the overview page shows: agents counted without the revoked ones, and the first steps. */
data class OverviewView(
    val agentsOnline: Int,
    val agentsTotal: Int,
    val firstSteps: FirstSteps,
)

/** The overview of a tenant for the console (W2 К14); [presence] decides who is online, as in the agent list. */
class Overviews(
    private val sessions: TenantSessions,
    private val presence: AgentPresence,
) {
    fun of(tenantId: UUID): OverviewView =
        sessions.inTenant(tenantId) { session ->
            val agents = session.createSelectionQuery(LIVE_AGENTS, Array<Any?>::class.java).list()
            val online = presence.onlineIds()
            OverviewView(
                agentsOnline = agents.count { it[0] in online },
                agentsTotal = agents.size,
                firstSteps =
                    FirstSteps(
                        tokenIssued = exists(session, ANY_TOKEN),
                        agentConnected = agents.any { it[1] != null },
                        repositoryInitialized = exists(session, INITIALIZED),
                        sourceCreated = exists(session, LIVE_SOURCES),
                        backupSucceeded = exists(session, SUCCEEDED_RUNS),
                    ),
            )
        }

    private fun exists(
        session: Session,
        count: String,
    ): Boolean = session.createSelectionQuery(count, java.lang.Long::class.java).singleResult.toLong() > 0
}
