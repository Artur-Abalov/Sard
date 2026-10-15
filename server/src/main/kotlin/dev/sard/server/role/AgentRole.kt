// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.role

/**
 * What an agent may do, fixed when its enrollment token is issued and never changed (V1): a source
 * runs BACKUP, RESTORE and RUN; a verifier runs only VERIFY. [stored] is the form in the database,
 * in the certificate and in REST.
 */
enum class AgentRole(
    val stored: String,
) {
    SOURCE("source"),
    VERIFIER("verifier"),
    ;

    companion object {
        /** The role stored as [stored]; fails for anything else (the database CHECK allows nothing else). */
        fun of(stored: String): AgentRole = checkNotNull(parse(stored)) { "unknown agent role '$stored'" }

        /** The role spelled exactly [text], or null. */
        fun parse(text: String): AgentRole? = entries.firstOrNull { it.stored == text }
    }
}
