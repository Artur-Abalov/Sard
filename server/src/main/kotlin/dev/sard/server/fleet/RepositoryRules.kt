// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.registration.RecoveryKey
import dev.sard.server.role.AgentRole
import java.time.Instant
import java.util.UUID

private val PLAIN_NAME = Regex("[A-Za-z0-9._-]+")
private const val ADDRESS_PLACEHOLDER = "<address>"
private const val FILE_PLACEHOLDER = "<file>"
private const val LABEL_HEX_DIGITS = 8

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

/** A repository by its id with the flag the holders agree on, and what to run when it has no recovery key (КС5). */
data class RepositorySummary(
    val repositoryId: String,
    val recoveryKey: RecoveryKey,
    val reportedAt: Instant?,
    val reportedByAgentId: UUID?,
    val recoveryKeyCommand: String?,
    val holders: List<RepositoryHolder>,
)

/** What a verifier needs to hold a repository its sources hold: a source to ask and the commands (КС7). */
data class VerifierSetupItem(
    val repositoryId: String,
    val sourceAgentId: UUID,
    val sourceHostname: String,
    val repositoryName: String,
    val keyAddCommand: String?,
    val repoAddTemplate: String?,
)

/** The V3 decisions about repositories as pure functions of what the agents reported: no database, no clock. */
object RepositoryRules {
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

    /** The repositories the sources hold and [verifier] does not, by id (КС7). */
    fun verifierSetup(
        holders: List<RepositoryHolder>,
        verifier: UUID,
    ): List<VerifierSetupItem> =
        holders
            .groupBy { it.repositoryId }
            .toSortedMap()
            .filterValues { group -> group.none { it.agentId == verifier } }
            .mapNotNull { (_, group) -> latestSource(group)?.let { setupItem(it, verifier) } }

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
        return source?.let { recoveryCommand(it, selfCommand) }
    }

    private fun recoveryCommand(
        source: RepositoryHolder,
        selfCommand: (String) -> String,
    ): String? =
        if (source.builtin) {
            selfCommand(source.repositoryName)
        } else {
            word(source.repositoryName)?.let {
                "sudo sard-agent repo key add $it --for recovery"
            }
        }

    private fun setupItem(
        source: RepositoryHolder,
        verifier: UUID,
    ): VerifierSetupItem {
        val name = word(source.repositoryName)
        val label = "vrf-" + verifier.toString().replace("-", "").take(LABEL_HEX_DIGITS)
        return VerifierSetupItem(
            source.repositoryId,
            source.agentId,
            source.hostname,
            source.repositoryName,
            name?.let { "sudo sard-agent repo key add $it --for verifier --label $label" },
            name?.let { "sudo sard-agent repo add $it $ADDRESS_PLACEHOLDER --password-from-file $FILE_PLACEHOLDER" },
        )
    }

    /** The holder of the latest Register; at the same time the smaller agent id. */
    private fun latest(holders: List<RepositoryHolder>): RepositoryHolder? =
        holders.minWithOrNull(
            compareByDescending<RepositoryHolder> { it.registeredAt }.thenBy { it.agentId.toString() },
        )

    private fun latestSource(all: List<RepositoryHolder>) = latest(all.filter { it.role == AgentRole.SOURCE })

    /** [name] as one shell word: bare, or in single quotes if it has other characters; null when no quoting is safe. */
    private fun word(name: String): String? =
        when {
            PLAIN_NAME.matches(name) -> name
            name.any { it == '\'' || it.isISOControl() } -> null
            else -> "'$name'"
        }
}
