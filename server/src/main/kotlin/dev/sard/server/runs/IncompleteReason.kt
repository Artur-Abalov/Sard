// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

/** Why a snapshot of a failed backup is incomplete (PL7-2 НБ6); the server decides, the console only shows it. */
enum class IncompleteReason(
    val stored: String,
) {
    /** restic could not read some files of the source. */
    UNREADABLE_FILES("unreadable_files"),

    /** The main snapshot is whole, but the fingerprint or the global objects snapshot linked to it failed. */
    SNAPSHOT_SET_INCOMPLETE("snapshot_set_incomplete"),

    /** An agent older than PL7-2 did not say how many files it could not read. */
    UNKNOWN("unknown"),
    ;

    companion object {
        /** The server's rule (НБ6): whole backups have none; the agent's count of unreadable paths decides the rest. */
        fun of(
            partial: Boolean,
            unreadableCount: Long?,
        ): IncompleteReason? =
            when {
                !partial -> null
                unreadableCount == null -> UNKNOWN
                unreadableCount == 0L -> SNAPSHOT_SET_INCOMPLETE
                else -> UNREADABLE_FILES
            }

        fun ofStored(stored: String?): IncompleteReason? = entries.firstOrNull { it.stored == stored }
    }
}
