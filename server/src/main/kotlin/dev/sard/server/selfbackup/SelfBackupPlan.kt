// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import dev.sard.server.runs.SystemRole

/** One system source as the server wants it; [config] holds secret names only (ADR 0008). */
internal data class PlannedSource(
    val role: SystemRole,
    val name: String,
    val plugin: String,
    val config: Map<String, Any>,
)

/**
 * The two system sources of the self-backup (F6): the database through the postgresql plugin and the CA
 * with the installation's configuration through the files plugin. They are backed up by separate runs.
 * Global objects are left out: the roles come back from the image's initialisation and the migrations.
 */
internal class SelfBackupPlan(
    private val settings: SelfBackupSettings,
) {
    fun sources(): List<PlannedSource> = SystemRole.entries.map(::source)

    fun source(role: SystemRole): PlannedSource =
        when (role) {
            SystemRole.SELF_DATABASE -> PlannedSource(role, "Sard: database", "postgresql", database())
            SystemRole.SELF_KEYS -> PlannedSource(role, "Sard: keys and configuration", "files", keys())
        }

    private fun database(): Map<String, Any> =
        with(settings) {
            mapOf(
                "host" to database.host,
                "port" to database.port,
                "database" to database.name,
                "user" to databaseUser,
                "password_ref" to passwordSecret,
                "tls_mode" to databaseTlsMode,
                "pg_dump_path" to pgDumpPath,
                "include_globals" to false,
            )
        }

    private fun keys(): Map<String, Any> =
        mapOf(
            "paths" to listOf(settings.pkiDir, settings.installDir),
            "exclude" to settings.installExclude,
        )
}
