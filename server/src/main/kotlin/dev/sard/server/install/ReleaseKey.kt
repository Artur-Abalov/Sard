// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

private const val RESOURCE = "/release/sard-release.pub"
private val COMMENT = Regex("""untrusted comment: minisign public key ([0-9A-F]{16})""")
private val KEY = Regex("""R[A-Za-z0-9+/]{55}""")

/**
 * The key the agent packages are signed with (U1b, В5): deploy/release/sard-release.pub, put into the
 * server build as a resource. The console shows it next to the signature step and tells to compare it
 * with README.md, which is outside the server; nothing is taken from SHA256SUMS.minisig.
 */
data class ReleaseKey(
    /** The key ID minisign prints, 16 hex digits. */
    val id: String,
    /** The second line of the key file, the string `minisign -P` takes. */
    val publicKey: String,
) {
    companion object {
        fun parse(file: String): ReleaseKey {
            val (comment, key) = file.lines().let { it.getOrNull(0).orEmpty() to it.getOrNull(1).orEmpty() }
            val id = checkNotNull(COMMENT.matchEntire(comment)) { "the release key file has no minisign comment line" }
            check(KEY.matches(key)) { "the release key file has no public key line" }
            return ReleaseKey(id.groupValues[1], key)
        }

        /** The key of this build; a build without it does not start. */
        fun bundled(): ReleaseKey {
            val stream = ReleaseKey::class.java.getResourceAsStream(RESOURCE)
            checkNotNull(stream) { "$RESOURCE is not in the build" }
            return parse(stream.use { String(it.readAllBytes(), Charsets.UTF_8) })
        }
    }
}
