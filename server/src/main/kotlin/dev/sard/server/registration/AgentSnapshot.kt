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
    /**
     * hex(HMAC-SHA256("sard-machine-id/v1" || tenant_id, raw machine id)): 64 lower-case hex characters. Required;
     * stored and searched, never shown in REST or logged.
     */
    val machineId: String,
    /** The directory the host allows restores into (R2); empty for an agent without console restore. */
    val restoreDir: String = "",
    /** RegisterRequest.verifier; null from an agent older than V4, which is never handed a verification. */
    val verifier: VerifierReport? = null,
) {
    /** Never the machine id: the snapshot is logged when something goes wrong. */
    override fun toString() = "AgentSnapshot(hostname=$hostname, agentVersion=$agentVersion)"
}

data class PluginEntry(
    val name: String,
    val version: String,
    /** JSON Schema text; only its syntax and size are checked. */
    val configSchema: String,
    /** Null stands for an action this server does not know (unspecified or newer than it). */
    val actions: List<PluginAction?>,
    /** JSON Schema text of the restore config; empty when the plugin cannot be restored from the console. */
    val restoreConfigSchema: String = "",
)

data class RepositoryEntry(
    val name: String,
    /** restic backend scheme ("s3", "sftp", "local", "rest", ...). */
    val backend: String,
    /** restic repository id; empty when the agent could not read it. */
    val repositoryId: String,
    /** Empty means restic's built-in AES (ADR 0008). */
    val cryptoProvider: String,
    /** Whether the repository has a recovery key, as the agent read it on this Register (V3). */
    val recoveryKey: RecoveryKey = RecoveryKey.UNKNOWN,
)

/**
 * The recovery key flag of a repository (V3). [UNKNOWN] is an agent older than V3 and an agent that could not read
 * the keys: it is never read as [ABSENT]. [stored] is the form in the database and in REST.
 */
enum class RecoveryKey(
    val stored: String,
) {
    PRESENT("present"),
    ABSENT("absent"),
    UNKNOWN("unknown"),
    ;

    companion object {
        /** The flag stored as [stored]; fails for anything else (the database CHECK allows nothing else). */
        fun of(stored: String): RecoveryKey =
            checkNotNull(entries.firstOrNull { it.stored == stored }) { "unknown recovery key flag '$stored'" }
    }
}

/** What a verifier reports about its host (V4); null fields are unknown to the agent. */
data class VerifierReport(
    val workFreeBytes: Long?,
    val sandbox: SandboxKind?,
)

/** The container runtime a verifier has; [stored] is agents.verify_sandbox. */
enum class SandboxKind(
    val stored: String,
) {
    NONE("none"),
    DOCKER("docker"),
    PODMAN("podman"),
}

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
