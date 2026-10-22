// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.registration.RecoveryKey
import java.time.Instant
import java.util.UUID

/** A repository by its id with the flag the holders agree on, and what to run when it has no recovery key (КС5). */
data class RepositorySummary(
    val repositoryId: String,
    val recoveryKey: RecoveryKey,
    val reportedAt: Instant?,
    val reportedByAgentId: UUID?,
    val recoveryKeyCommand: String?,
    val holders: List<RepositoryHolder>,
)

/** The V3 decisions about the recovery key of a repository, as pure functions of what the agents reported. */
object RecoveryKeyRules {
    /** One summary per repository id, by id; [selfCommand] builds the command for the built-in agent's repository. */
    fun summaries(
        holders: List<RepositoryHolder>,
        selfCommand: (String) -> String,
    ): List<RepositorySummary> =
        holders
            .groupBy { it.repositoryId }
            .toSortedMap()
            .map { (id, group) -> summaryOf(id, onePerAgent(group), selfCommand) }

    /** The summaries whose repository has no recovery key; "unknown" does not count (КС5). */
    fun withoutRecoveryKey(items: List<RepositorySummary>): Int = items.count { it.recoveryKey == RecoveryKey.ABSENT }

    /** An agent that holds a repository under two names is one holder, with the first name. */
    private fun onePerAgent(group: List<RepositoryHolder>): List<RepositoryHolder> =
        group
            .groupBy { it.agentId }
            .map { (_, own) -> own.minBy { it.repositoryName } }
            .sortedBy { it.agentId.toString() }

    private fun summaryOf(
        id: String,
        holders: List<RepositoryHolder>,
        selfCommand: (String) -> String,
    ): RepositorySummary {
        val reporter = latest(holders.filter { it.recoveryKey != RecoveryKey.UNKNOWN })
        if (reporter == null) return RepositorySummary(id, RecoveryKey.UNKNOWN, null, null, null, holders)
        val command = commandFor(reporter.recoveryKey, holders, selfCommand)
        return RepositorySummary(id, reporter.recoveryKey, reporter.registeredAt, reporter.agentId, command, holders)
    }

    /** Only a repository without a recovery key gets one, and only a source can issue it (КС6). */
    private fun commandFor(
        flag: RecoveryKey,
        holders: List<RepositoryHolder>,
        selfCommand: (String) -> String,
    ): String? {
        val source = latestSource(holders).takeIf { flag == RecoveryKey.ABSENT }
        return source?.let {
            if (it.builtin) selfCommand(it.repositoryName) else recoveryKeyAddCommand(it.repositoryName)
        }
    }
}
