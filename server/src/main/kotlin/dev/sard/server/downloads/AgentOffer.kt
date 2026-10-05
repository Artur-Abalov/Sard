// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.downloads

/** What this server hands out to hosts (SARD_AGENT_DOWNLOADS): its release, or nothing. */
sealed interface AgentOffer {
    /** The agent version the server offers: its release's, or its own build's when it offers none. */
    val version: String

    data class Serving(
        val catalog: AgentPackageCatalog,
    ) : AgentOffer {
        override val version: String get() = catalog.version
    }

    /** Downloads are off: no packages are at hand, the server's own version is all that is known. */
    data class Withheld(
        override val version: String,
    ) : AgentOffer
}
