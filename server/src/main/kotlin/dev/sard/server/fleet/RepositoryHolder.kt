// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.registration.RecoveryKey
import dev.sard.server.role.AgentRole
import java.time.Instant
import java.util.UUID

/** One agent's report of one repository: the agent and the last Register it came from (V3). */
data class RepositoryHolder(
    val agentId: UUID,
    val hostname: String,
    val role: AgentRole,
    val builtin: Boolean,
    val repositoryId: String,
    val repositoryName: String,
    val registeredAt: Instant,
    val recoveryKey: RecoveryKey,
)

/** The holder of the latest Register; at the same time the smaller agent id. */
internal fun latest(holders: List<RepositoryHolder>): RepositoryHolder? =
    holders.minWithOrNull(
        compareByDescending<RepositoryHolder> { it.registeredAt }.thenBy { it.agentId.toString() },
    )

/** The latest holder that is a source: only a source can issue a key or be asked for one. */
internal fun latestSource(all: List<RepositoryHolder>): RepositoryHolder? {
    val sources = all.filter { it.role == AgentRole.SOURCE }
    return latest(sources)
}
