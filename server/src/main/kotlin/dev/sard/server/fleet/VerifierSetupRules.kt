// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import java.util.UUID

/** What a verifier needs to hold a repository its sources hold: a source to ask and the commands (КС7). */
data class VerifierSetupItem(
    val repositoryId: String,
    val sourceAgentId: UUID,
    val sourceHostname: String,
    val repositoryName: String,
    val keyAddCommand: String?,
    val repoAddTemplate: String?,
)

/** The V3 decision of what a verifier is offered to hold, as a pure function of what the agents reported. */
object VerifierSetupRules {
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

    private fun setupItem(
        source: RepositoryHolder,
        verifier: UUID,
    ) = VerifierSetupItem(
        source.repositoryId,
        source.agentId,
        source.hostname,
        source.repositoryName,
        verifierKeyAddCommand(source.repositoryName, verifier),
        verifierRepoAddTemplate(source.repositoryName),
    )
}

/** The agent a source runs on and the repository it writes to: what a verifier needs a key for (V6). */
data class SourceRepository(
    val agentId: UUID,
    val hostname: String,
    val repositoryName: String,
)

/** One command for a source host: issue a key for the verifier to come. */
data class FirstVerifierItem(
    val sourceAgentId: UUID,
    val sourceHostname: String,
    val repositoryName: String,
    val keyAddCommand: String?,
)

/** What to do to get the first verifier going (V6, rule 13); the passwords never pass through the console. */
data class FirstVerifierSetup(
    val repositories: List<FirstVerifierItem>,
    /** The command to run on the verifier for each repository, with placeholders for what only its host knows. */
    val verifierRepoAdd: String,
) {
    companion object {
        fun of(sourceRepositories: List<SourceRepository>): FirstVerifierSetup =
            FirstVerifierSetup(
                sourceRepositories
                    .distinct()
                    .sortedWith(compareBy({ it.hostname }, { it.repositoryName }, { it.agentId }))
                    .map {
                        val command = firstVerifierKeyAddCommand(it.repositoryName)
                        FirstVerifierItem(it.agentId, it.hostname, it.repositoryName, command)
                    },
                FIRST_VERIFIER_REPO_ADD,
            )
    }
}
