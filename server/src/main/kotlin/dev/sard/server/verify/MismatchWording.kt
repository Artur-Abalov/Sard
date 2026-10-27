// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

/** The fixed set of facts a mismatch can be about; [wire] is its name in the API. */
enum class MismatchKind(
    val wire: String,
) {
    ROWS("rows"),
    TABLES("tables"),
    EXTENSION("extension"),
    EXTENSION_VERSION("extension_version"),
    CONTROL_PATH("control_path"),
    FILES("files"),
    BYTES("bytes"),
    FILE_HASH("file_hash"),
    OTHER("other"),
}

/** What a mismatch is about: the [kind] of fact and its [subject] (a table, a path, an extension); empty for none. */
data class MismatchWording(
    val kind: MismatchKind,
    val subject: String,
) {
    companion object {
        /** Facts named «prefix:subject». */
        private val WITH_SUBJECT =
            mapOf(
                "rows" to MismatchKind.ROWS,
                "extension" to MismatchKind.EXTENSION,
                "extension_version" to MismatchKind.EXTENSION_VERSION,
                "control" to MismatchKind.CONTROL_PATH,
                "sha256" to MismatchKind.FILE_HASH,
            )

        /** Facts named by their kind alone. */
        private val ALONE =
            mapOf("tables" to MismatchKind.TABLES, "files" to MismatchKind.FILES, "bytes" to MismatchKind.BYTES)

        /** The kind and subject of the fact named [name] (V2 fingerprint facts); an unknown name is its own subject. */
        fun of(name: String): MismatchWording {
            val prefix = name.substringBefore(':')
            val subject = name.substringAfter(':', "")
            val alone = ALONE[name]
            val kind = WITH_SUBJECT[prefix]
            return when {
                alone != null -> MismatchWording(alone, "")
                kind != null && subject.isNotEmpty() -> MismatchWording(kind, subject)
                else -> MismatchWording(MismatchKind.OTHER, name)
            }
        }
    }
}
