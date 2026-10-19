// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import tools.jackson.databind.JsonNode
import java.time.Instant
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter

/**
 * What the console knows of the restore of one plugin (ВК32a, closes OQ-220): the config a form starts from,
 * where the restore goes, which parts of the snapshot it takes, whether it restores global objects, and what it
 * warns about. The code that starts a restore and checks the config reads none of the keys of a plugin: it asks
 * the entry of the plugin here.
 */
internal interface RestorePlugin {
    /** Whether the restore goes into a directory below the restore directory of the host, not into a database. */
    val intoDirectory: Boolean

    /** The config the form starts from; [backupConfig] is the copy of the config of the step that made the snapshot. */
    fun defaults(
        sourceName: String,
        snapshotAt: Instant,
        backupConfig: JsonNode,
    ): Map<String, Any>

    /** Where the restore goes: an absolute path of the restore directory, or a name of a database. */
    fun target(
        restoreDir: String,
        config: JsonNode,
    ): String

    fun paths(config: JsonNode): List<String>

    fun restoreGlobals(config: JsonNode): Boolean

    /** What the preview warns about; [fromBackupAgent]: the agent that restores made the backup. */
    fun warnings(
        config: JsonNode,
        backupConfig: JsonNode,
        fromBackupAgent: Boolean,
    ): List<RestoreWarning>
}

/** The plugins the console restores, one entry each. */
internal object RestorePlugins {
    private val ALL: Map<String, RestorePlugin> =
        mapOf(FILES_PLUGIN to FilesRestore, POSTGRESQL_PLUGIN to PostgresqlRestore)

    fun of(plugin: String): RestorePlugin? = ALL[plugin]

    fun restores(plugin: String): Boolean = plugin in ALL
}

/** The plugin whose restore the console writes (R2a). */
internal const val FILES_PLUGIN = "files"

/** The plugin whose restore into a new database the console writes (R2b). */
internal const val POSTGRESQL_PLUGIN = "postgresql"

/** A files restore goes to a new directory below the restore directory of the host (ВК32). */
private object FilesRestore : RestorePlugin {
    override val intoDirectory = true

    override fun defaults(
        sourceName: String,
        snapshotAt: Instant,
        backupConfig: JsonNode,
    ): Map<String, Any> = mapOf("destination" to RestoreDefaults.destination(sourceName, snapshotAt))

    override fun target(
        restoreDir: String,
        config: JsonNode,
    ): String = restoreDir.trimEnd('/') + "/" + config.path("destination").asString()

    override fun paths(config: JsonNode): List<String> =
        config
            .path("paths")
            .iterator()
            .asSequence()
            .map { it.asString() }
            .toList()

    override fun restoreGlobals(config: JsonNode): Boolean = false

    override fun warnings(
        config: JsonNode,
        backupConfig: JsonNode,
        fromBackupAgent: Boolean,
    ): List<RestoreWarning> = listOf(RestoreWarning.OWNERS_NOT_RESTORED)
}

/** A PostgreSQL restore goes into a new database of the server the config names (ВК32, ВК32a, ВК32b). */
private object PostgresqlRestore : RestorePlugin {
    override val intoDirectory = false

    private val CONNECTION = listOf("host", "port", "user", "password_ref", "tls_mode", "tls_root_cert")
    private val DAY = DateTimeFormatter.ofPattern("yyyyMMdd").withZone(ZoneOffset.UTC)
    private const val MAX_NAME_BYTES = 63
    private const val DEFAULT_PORT = 5432

    override fun defaults(
        sourceName: String,
        snapshotAt: Instant,
        backupConfig: JsonNode,
    ): Map<String, Any> {
        val connection = CONNECTION.filter { backupConfig.has(it) }.associateWith { plain(backupConfig.path(it)) }
        val database = backupConfig.path("database").asString().ifEmpty { sourceName }
        return connection + mapOf("database" to newName(database, snapshotAt), "restore_globals" to false)
    }

    /** A JSON scalar as Kotlin has it: the port stays a number. */
    private fun plain(node: JsonNode): Any = if (node.isNumber) node.asInt() else node.asString()

    /** `<database>_restored_<day of the snapshot>`, the database cut at a character so that the name fits. */
    private fun newName(
        database: String,
        snapshotAt: Instant,
    ): String {
        val suffix = "_restored_" + DAY.format(snapshotAt)
        return cut(database, MAX_NAME_BYTES - suffix.toByteArray().size) + suffix
    }

    /** [text] cut to at most [bytes] bytes of UTF-8 at the end of a character. */
    private fun cut(
        text: String,
        bytes: Int,
    ): String {
        var used = 0
        val kept = StringBuilder()
        for (codePoint in text.codePoints()) {
            val size = String(Character.toChars(codePoint)).toByteArray().size
            if (used + size > bytes) break
            used += size
            kept.appendCodePoint(codePoint)
        }
        return kept.toString()
    }

    override fun target(
        restoreDir: String,
        config: JsonNode,
    ): String = config.path("database").asString()

    override fun paths(config: JsonNode): List<String> = emptyList()

    override fun restoreGlobals(config: JsonNode): Boolean = config.path("restore_globals").asBoolean(false)

    override fun warnings(
        config: JsonNode,
        backupConfig: JsonNode,
        fromBackupAgent: Boolean,
    ): List<RestoreWarning> {
        val kind =
            if (restoreGlobals(config)) {
                RestoreWarning.GLOBALS_CHANGE_CLUSTER
            } else {
                RestoreWarning.PG_OWNERS_NOT_RESTORED
            }
        val same = fromBackupAgent && sameServer(config, backupConfig)
        return listOfNotNull(kind, RestoreWarning.PG_SAME_CLUSTER.takeIf { same })
    }

    /** The host (without case) and the port of the backup, the port 5432 by default (ВК32b). */
    private fun sameServer(
        config: JsonNode,
        backupConfig: JsonNode,
    ): Boolean {
        val host = config.path("host").asString()
        return host.isNotEmpty() &&
            host.equals(backupConfig.path("host").asString(), ignoreCase = true) &&
            config.path("port").asInt(DEFAULT_PORT) == backupConfig.path("port").asInt(DEFAULT_PORT)
    }
}
