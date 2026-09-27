// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

/** The extensions present on the classpath; empty in the open core. */
class ExtensionRegistry(
    extensions: List<SardExtension>,
) {
    /** Extensions ordered by id. */
    val extensions: List<SardExtension> = extensions.sortedBy { it.id }

    init {
        val duplicates = extensions.groupBy { it.id }.filterValues { it.size > 1 }.keys
        require(duplicates.isEmpty()) { "Duplicate Sard extension ids: ${duplicates.sorted()}" }
    }

    fun ids(): List<String> = extensions.map { it.id }
}
