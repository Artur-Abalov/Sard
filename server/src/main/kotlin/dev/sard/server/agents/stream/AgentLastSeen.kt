// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import dev.sard.server.agents.AgentSessions
import java.time.Instant

private const val TOUCH =
    "update Agent a set a.lastSeenAt = :at where a.id = :id and (a.lastSeenAt is null or a.lastSeenAt < :at)"

/** `agents.last_seen_at` in the agent's own tenant (S3's [AgentSessions]); never moves it back. */
class AgentLastSeen(
    private val sessions: AgentSessions,
) : LastSeenStore {
    override fun record(
        agent: ConnectedAgent,
        at: Instant,
    ) {
        sessions.inTenant { session ->
            session
                .createMutationQuery(TOUCH)
                .setParameter("at", at)
                .setParameter("id", agent.agentId)
                .executeUpdate()
        }
    }
}
