// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.registration

import tools.jackson.databind.json.JsonMapper

private val JSON = JsonMapper.builder().build()

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
    /** Every secret of the host with its binding to a plugin and its targets (S11); names and targets only. */
    val secrets: List<SecretEntry> = emptyList(),
) {
    /** [secrets] as agents.secret_bindings stores them. */
    fun secretsJson(): String = JSON.writeValueAsString(secrets.map { it.stored() })

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
    /** The runtime's version as its CLI reports it (V5). */
    val sandboxVersion: String? = null,
    /** Sandbox images the verifier has (V5). */
    val images: List<SandboxImageEntry> = emptyList(),
) {
    /** [images] as agents.verify_images stores them. */
    fun imagesJson(): String =
        JSON.writeValueAsString(images.map { mapOf("name" to it.name, "version" to it.version, "digest" to it.digest) })
}

/** A sandbox image a verifier has: its name, the semantic version and the digest it runs it by (V5). */
data class SandboxImageEntry(
    val name: String,
    val version: String,
    val digest: String,
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

/**
 * A secret of the host and its binding (S11, RegisterRequest.secrets): the plugin and the targets the operator of
 * the host allows its value to go to, and what the binding allows beyond the plugin's rules. [problem] says why the
 * agent gives the secret to no plugin; it is empty when the binding is in force.
 */
data class SecretEntry(
    val name: String,
    val plugin: String = "",
    val targets: List<String> = emptyList(),
    val auth: List<String> = emptyList(),
    val allowTls: String = "",
    val allowNoChannelBinding: Boolean = false,
    val problem: String = "",
) {
    fun stored(): Map<String, Any> =
        mapOf(
            "name" to name,
            "plugin" to plugin,
            "targets" to targets,
            "auth" to auth,
            "allow_tls" to allowTls,
            "allow_no_channel_binding" to allowNoChannelBinding,
            "problem" to problem,
        )

    /** Every text of the entry, for the rules of storage. */
    fun texts(): List<String> = listOf(name, plugin, allowTls, problem) + targets + auth
}
