// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.downloads

/** The agent packages cannot be served: the server does not start (docs/adr/0040-agent-release.md). */
class AgentPackagesUnavailable(
    message: String,
    cause: Throwable? = null,
) : IllegalStateException(message, cause)

/** One package of `manifest.json`, as scripts/package-agent.sh writes it. */
data class AgentArtifact(
    val file: String,
    val size: Long,
    val sha256: String,
)

/** What the server needs of `manifest.json`; the rest is for its readers (the console, people). */
data class AgentManifest(
    val schema: Int,
    val version: String,
    val artifacts: List<AgentArtifact>,
)

/**
 * The files of one agent release the server hands out, each with its SHA-256 as ETag. Only
 * packages of the server's own version are served: a server never offers another agent.
 */
class AgentPackageCatalog private constructor(
    val version: String,
    private val etags: Map<String, String>,
) {
    /** Every file name that may be requested. */
    val files: Set<String> get() = etags.keys

    fun etag(file: String): String? = etags[file]

    companion object {
        const val MANIFEST = "manifest.json"
        const val SUMS = "SHA256SUMS"
        const val SIGNATURE = "SHA256SUMS.minisig"
        private const val SCHEMA = 1
        private val PLAIN_NAME = Regex("[A-Za-z0-9][A-Za-z0-9._~+-]*")

        /** [metadata]: SHA-256 of manifest.json, SHA256SUMS and, in a signed release, SHA256SUMS.minisig. */
        fun of(
            manifest: AgentManifest,
            serverVersion: String,
            metadata: Map<String, String>,
        ): AgentPackageCatalog {
            requireMetadata(metadata)
            requireShape(manifest)
            check(manifest.version == serverVersion) {
                "agent packages are version ${manifest.version}, the server is $serverVersion"
            }
            val packages = manifest.artifacts.associate { it.file to it.sha256 }
            return AgentPackageCatalog(manifest.version, packages + metadata)
        }

        private fun requireMetadata(metadata: Map<String, String>) {
            listOf(MANIFEST, SUMS).firstOrNull { it !in metadata }?.let {
                throw AgentPackagesUnavailable("$it is missing")
            }
        }

        private fun requireShape(manifest: AgentManifest) {
            check(manifest.schema == SCHEMA) {
                "manifest.json has schema ${manifest.schema}, this server reads $SCHEMA"
            }
            check(manifest.artifacts.isNotEmpty()) { "manifest.json lists no artifacts" }
            manifest.artifacts.firstOrNull { !PLAIN_NAME.matches(it.file) }?.let {
                throw AgentPackagesUnavailable("manifest.json: \"${it.file}\" is not a plain file name")
            }
        }

        private inline fun check(
            condition: Boolean,
            message: () -> String,
        ) {
            if (!condition) throw AgentPackagesUnavailable(message())
        }
    }
}
