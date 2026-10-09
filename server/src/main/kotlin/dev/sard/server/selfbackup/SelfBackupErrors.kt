// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

/** Why the self-backup refused; the REST layer maps each one to its problem. */
sealed class SelfBackupException(
    message: String,
) : RuntimeException(message)

/** The tenant has no live built-in agent to back it up (422 self_agent_missing). */
class SelfAgentMissing : SelfBackupException("no live built-in agent")

/** The repository has no restic id in the agent's last Register yet (422 repository_not_initialized). */
class RepositoryNotInitialized(
    val repositoryName: String,
) : SelfBackupException("repository $repositoryName is not initialised")

/** A local repository on the server's machine needs an explicit confirmation (D10, 422 local_storage_unconfirmed). */
class LocalStorageUnconfirmed(
    val repositoryName: String,
) : SelfBackupException("repository $repositoryName is local and not confirmed")

/** "Back up now" before a repository is bound (409 self_backup_not_configured). */
class SelfBackupNotConfigured : SelfBackupException("the self-backup is not configured")
