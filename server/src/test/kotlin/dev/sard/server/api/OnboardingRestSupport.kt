// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.agents.stream.TestAgent
import dev.sard.server.completeOnboarding
import dev.sard.server.registration.PluginAction
import dev.sard.server.registration.PluginEntry
import dev.sard.server.registration.RepositoryEntry
import org.springframework.jdbc.core.JdbcTemplate
import java.io.File
import java.util.UUID

private val POSTGRESQL_SCHEMA: String by lazy { File("../agent/plugins/postgresql/schema.json").readText() }
private val BACKUP = listOf(PluginAction.BACKUP, PluginAction.RESTORE)

/** The id restic would have read from an initialised repository. */
const val SELF_REPOSITORY_ID = "0f3a0f3a0f3a0f3a0f3a0f3a0f3a0f3a0f3a0f3a0f3a0f3a0f3a0f3a0f3a0f3a"

/** What the built-in agent of the compose installation has initialised: S3, SFTP, and a local one. */
fun selfRepositories(): List<RepositoryEntry> =
    listOf(
        RepositoryEntry("self", "s3", SELF_REPOSITORY_ID, ""),
        RepositoryEntry("self-2", "sftp", SELF_REPOSITORY_ID.reversed(), ""),
        RepositoryEntry("disk", "local", "e".repeat(64), ""),
    )

/** The built-in agent of [tenant] with the plugins and the secret the self-backup needs. */
fun RestWorld.selfAgent(
    tenant: UUID,
    repositories: List<RepositoryEntry> = selfRepositories(),
): TestAgent =
    agent(
        tenant,
        snapshotOf(
            plugins = listOf(filesPlugin(), PluginEntry("postgresql", "0.1.0", POSTGRESQL_SCHEMA, BACKUP)),
            repositories = repositories,
            secretNames = listOf("sard-db"),
        ),
        hostname = "sard-self",
        builtin = true,
    )

/** The onboarding as a clean installation leaves it after the wizard of F4a: nothing of F4b is done. */
fun JdbcTemplate.startOnboarding() {
    update("delete from onboarding_steps where step = 'keys_confirmed'")
    update("delete from onboarding_marks")
}

/** Back to what the other REST tests expect: the onboarding is complete. */
fun JdbcTemplate.finishOnboarding() {
    update("delete from onboarding_marks")
    completeOnboarding(this)
}

/** An installation that was upgraded with agents and sources (Р21). */
fun JdbcTemplate.markUpgraded() {
    update("insert into onboarding_marks (mark, marked_at) values ('upgraded', now()) on conflict do nothing")
}
