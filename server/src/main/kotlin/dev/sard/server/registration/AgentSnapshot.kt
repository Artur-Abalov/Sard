// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.registration

/**
 * What an agent announces about itself in Register (ADR 0008: names and metadata only,
 * never a secret value, a key or a URL with credentials). Strings are as received; empty
 * means "not reported" where the contract allows it.
 */
data class AgentSnapshot(
    val hostname: String,
    val agentVersion: String,
    /** Unsigned on the wire; a Long so no value wraps around. */
    val protocolVersion: Long,
    val os: String,
    val arch: String,
    val plugins: List<PluginEntry>,
    val repositories: List<RepositoryEntry>,
    val secretNames: List<String>,
    val scriptNames: List<String>,
)

data class PluginEntry(
    val name: String,
    val version: String,
    /** JSON Schema text; only its syntax and size are checked. */
    val configSchema: String,
    /** Null stands for an action this server does not know (unspecified or newer than it). */
    val actions: List<PluginAction?>,
)

data class RepositoryEntry(
    val name: String,
    /** restic backend scheme ("s3", "sftp", "local", "rest", ...). */
    val backend: String,
    /** restic repository id; empty when the agent could not read it. */
    val repositoryId: String,
    /** Empty means restic's built-in AES (ADR 0008). */
    val cryptoProvider: String,
)

/** Actions a plugin implements; [stored] is the lower_snake_case form in the database (ADR 0013, rule 7). */
enum class PluginAction(
    val stored: String,
) {
    BACKUP("backup"),
    RESTORE("restore"),
    VERIFY("verify"),
    RUN("run"),
}

/** The one place the protocol versions this server speaks are defined (RegisterRequest.protocol_version). */
object ProtocolVersions {
    val SUPPORTED: LongRange = 1L..1L
}
