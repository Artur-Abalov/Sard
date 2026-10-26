// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

/** What a mismatch is about: the [kind] of fact and its [subject] (a table, a path, an extension); empty for none. */
data class MismatchWording(
    val kind: String,
    val subject: String,
) {
    companion object {
        /** Facts named «prefix:subject». */
        private val WITH_SUBJECT =
            mapOf(
                "rows" to "rows",
                "extension" to "extension",
                "extension_version" to "extension_version",
                "control" to "control_path",
                "sha256" to "file_hash",
            )

        /** Facts named by their kind alone. */
        private val ALONE = setOf("tables", "files", "bytes")

        /** The kind and subject of the fact named [name] (V2 fingerprint facts); an unknown name is its own subject. */
        fun of(name: String): MismatchWording {
            val prefix = name.substringBefore(':')
            val subject = name.substringAfter(':', "")
            val kind = WITH_SUBJECT[prefix]
            return when {
                name in ALONE -> MismatchWording(name, "")
                kind != null && subject.isNotEmpty() -> MismatchWording(kind, subject)
                else -> MismatchWording("other", name)
            }
        }
    }
}
