// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import java.nio.charset.StandardCharsets
import java.time.Instant
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter
import java.util.UUID

/** Why the console does not restore a snapshot (R2, КВ1); the order is the order the reasons are tried in. */
enum class RestoreBlock(
    val stored: String,
) {
    PARTIAL("partial"),
    FORGOTTEN("forgotten"),
    PLUGIN_NOT_SUPPORTED("plugin_not_supported"),
}

/** The plugin whose restore the console writes (R2a). */
internal const val FILES_PLUGIN = "files"

/** Which snapshots the console restores: complete, still in the repository, of a plugin whose restore is written. */
internal object RestoreSupport {
    /** The plugins the console restores: files (R2a); postgresql comes with R2b. */
    private val PLUGINS = setOf(FILES_PLUGIN)

    fun blocked(
        plugin: String,
        partial: Boolean,
        forgotten: Boolean,
    ): RestoreBlock? =
        when {
            partial -> RestoreBlock.PARTIAL
            forgotten -> RestoreBlock.FORGOTTEN
            plugin !in PLUGINS -> RestoreBlock.PLUGIN_NOT_SUPPORTED
            else -> null
        }
}

/** The config a restore form starts from (ВК32). */
internal object RestoreDefaults {
    private val STAMP = DateTimeFormatter.ofPattern("yyyyMMdd'T'HHmmss'Z'").withZone(ZoneOffset.UTC)

    /** `<source name>/<snapshot time>`: the name's bytes outside `[A-Za-z0-9_-]` as `%XX`, the time in UTC. */
    fun destination(
        sourceName: String,
        snapshotTime: Instant,
    ): String = "${escaped(sourceName)}/${STAMP.format(snapshotTime)}"

    private const val SAFE = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"
    private const val BYTE_MASK = 0xFF

    private fun escaped(name: String): String =
        name.toByteArray(StandardCharsets.UTF_8).joinToString("") { byte ->
            val c = byte.toInt().toChar()
            if (c in SAFE) c.toString() else "%%%02X".format(byte.toInt() and BYTE_MASK)
        }
}

/** Why an agent with the snapshot's repository cannot restore it (КВ2). */
enum class IneligibleReason(
    val stored: String,
) {
    AGENT_OUTDATED("agent_outdated"),
    PLUGIN_MISSING("plugin_missing"),
}

/** What the console shows of the snapshot to restore. */
data class RestoreSnapshot(
    val snapshotId: String,
    val createdAt: Instant,
    val totalBytes: Long,
    val repositoryName: String,
    val sourceName: String,
    val plugin: String,
)

/** An agent that can restore the snapshot: [repositoryName] is the repository on this agent, [defaults] JSON text. */
data class RestoreTarget(
    val agentId: UUID,
    val hostname: String,
    val sourceAgent: Boolean,
    val repositoryName: String,
    val restoreDir: String,
    val restoreConfigSchema: String,
    val defaults: String,
)

/** An agent with the snapshot's repository that cannot restore it. */
data class IneligibleAgent(
    val agentId: UUID,
    val hostname: String,
    val reason: IneligibleReason,
)

/** Where to restore and with which config; [agentId] is as the caller wrote it, checked by the service. */
data class RestoreDraft(
    val agentId: String?,
    /** JSON object text. */
    val config: String,
)

data class RestoreOptions(
    val snapshot: RestoreSnapshot,
    val targets: List<RestoreTarget>,
    val ineligible: List<IneligibleAgent>,
    /** The repository the command attaches: the snapshot's own name for it. */
    val attachRepositoryName: String,
    /** The command to attach the repository: placeholders for the address and the password file (ADR 0008). */
    val attachCommand: String,
)

/** A warning of the preview that the domain knows; the console adds the one about an offline agent. */
enum class RestoreWarning(
    val stored: String,
) {
    OWNERS_NOT_RESTORED("owners_not_restored"),
    GLOBALS_CHANGE_CLUSTER("globals_change_cluster"),
}

/** What a restore request would do (КВ3); nothing is created. */
data class RestorePreview(
    val sourceName: String,
    val snapshotId: String,
    val createdAt: Instant,
    val totalBytes: Long,
    val agentId: UUID,
    val hostname: String,
    /** The absolute path of the directory, or the name of the database. */
    val target: String,
    val paths: List<String>,
    val restoreGlobals: Boolean,
    val warnings: List<RestoreWarning>,
)
