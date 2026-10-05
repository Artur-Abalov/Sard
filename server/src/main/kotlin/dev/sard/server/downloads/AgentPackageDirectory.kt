// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.downloads

import tools.jackson.core.JacksonException
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.nio.file.Path
import java.security.MessageDigest
import kotlin.io.path.fileSize
import kotlin.io.path.isDirectory
import kotlin.io.path.isRegularFile
import kotlin.io.path.readBytes

/**
 * Reads the release `make package` put into the image (deploy/server/Dockerfile) and checks it
 * before anything is served: manifest, SHA256SUMS, the server's version, every listed package
 * present with its size. Sums of the packages were checked when the image was built.
 */
object AgentPackageDirectory {
    private const val INVALID = "manifest.json is not a valid manifest"

    fun load(
        dir: Path,
        serverVersion: String,
        mapper: ObjectMapper,
    ): AgentPackageCatalog {
        if (!dir.isDirectory()) {
            throw AgentPackagesUnavailable(
                "no agent packages in $dir (sard.agent-packages.dir): build the image after make package, " +
                    "or set SARD_AGENT_DOWNLOADS=false to run without agent downloads",
            )
        }
        val manifest = manifest(dir, mapper)
        // Validates the file names before any is resolved against dir.
        val catalog = AgentPackageCatalog.of(manifest, serverVersion, metadata(dir))
        manifest.artifacts.forEach { checkPackage(dir.resolve(it.file), it.size) }
        return catalog
    }

    private fun metadata(dir: Path): Map<String, String> =
        listOf(AgentPackageCatalog.MANIFEST, AgentPackageCatalog.SUMS, AgentPackageCatalog.SIGNATURE)
            .map { dir.resolve(it) }
            .filter { it.isRegularFile() }
            .associate { it.fileName.toString() to sha256(it) }

    private fun manifest(
        dir: Path,
        mapper: ObjectMapper,
    ): AgentManifest {
        val file = dir.resolve(AgentPackageCatalog.MANIFEST)
        if (!file.isRegularFile()) throw AgentPackagesUnavailable("$file is missing")
        val root =
            try {
                mapper.readTree(file.readBytes())
            } catch (e: JacksonException) {
                throw AgentPackagesUnavailable("$file is not a valid manifest: ${e.originalMessage}", e)
            }
        return parse(root)
    }

    private fun parse(root: JsonNode) =
        AgentManifest(
            schema = field(root, "schema").asInt(),
            version = field(root, "version").asString(),
            artifacts =
                field(root, "artifacts").iterator().asSequence().toList().map {
                    AgentArtifact(
                        file = field(it, "file").asString(),
                        size = field(it, "size").asLong(),
                        sha256 = field(it, "sha256").asString(),
                    )
                },
        )

    private fun field(
        node: JsonNode,
        name: String,
    ): JsonNode = node.get(name) ?: throw AgentPackagesUnavailable("$INVALID: \"$name\" is missing")

    private fun checkPackage(
        file: Path,
        size: Long,
    ) {
        if (!file.isRegularFile()) throw AgentPackagesUnavailable("${file.fileName} is missing in ${file.parent}")
        val actual = file.fileSize()
        if (actual != size) throw AgentPackagesUnavailable("${file.fileName} is $actual bytes, the manifest says $size")
    }

    private fun sha256(file: Path): String = MessageDigest.getInstance("SHA-256").digest(file.readBytes()).toHexString()
}
