// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import tools.jackson.databind.json.JsonMapper

private val JSON = JsonMapper.builder().build()

/** What a source's config lacks so that its verification proves more (V6, rule 9). */
enum class VerificationHint(
    val stored: String,
) {
    /** PostgreSQL: without key tables the verification compares the 10 largest ones. */
    KEY_TABLES_MISSING("key_tables_missing"),

    /** Files: without control paths an unmounted disk restores as an empty directory and passes. */
    CONTROL_PATHS_MISSING("control_paths_missing"),
}

/** The hints of a source, from its plugin and its config as stored (JSON text). */
object VerificationHints {
    /** The config key that names what the plugin's verification must see, by plugin. */
    private val WANTED =
        mapOf(
            "postgresql" to ("key_tables" to VerificationHint.KEY_TABLES_MISSING),
            "files" to ("control_paths" to VerificationHint.CONTROL_PATHS_MISSING),
        )

    fun of(
        plugin: String,
        config: String,
    ): List<VerificationHint> {
        val (key, hint) = WANTED[plugin] ?: return emptyList()
        return listOf(hint).filter { JSON.readTree(config).path(key).size() == 0 }
    }
}
