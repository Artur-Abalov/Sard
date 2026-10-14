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

/** What a system source backs up, for the wizard to show before the binding; no secret names (F4b, Р26). */
data class PlanItem(
    val role: SystemRole,
    val name: String,
    val plugin: String,
    val includes: List<String>,
    val excludes: List<String>,
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

    /** The sources as the owner reads them: the database by name and address, the files by path. */
    fun items(): List<PlanItem> =
        SystemRole.entries.map { role ->
            val planned = source(role)
            when (role) {
                SystemRole.SELF_DATABASE -> {
                    PlanItem(role, planned.name, planned.plugin, listOf(databaseSummary()), emptyList())
                }

                SystemRole.SELF_KEYS -> {
                    PlanItem(
                        role,
                        planned.name,
                        planned.plugin,
                        listOf(settings.pkiDir, settings.installDir),
                        settings.installExclude,
                    )
                }
            }
        }

    private fun databaseSummary() = with(settings.database) { "$name на $host:$port" }

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
