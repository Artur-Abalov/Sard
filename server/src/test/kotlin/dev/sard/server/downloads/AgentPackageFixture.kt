// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.downloads

import java.nio.file.Files
import java.nio.file.Path
import java.security.MessageDigest
import kotlin.io.path.createDirectories
import kotlin.io.path.readBytes
import kotlin.io.path.writeBytes
import kotlin.io.path.writeText

/** A release directory shaped like `make package` output: two small packages, manifest.json, SHA256SUMS. */
object AgentPackageFixture {
    const val DEB = "sard-agent_%s_amd64.deb"
    const val TAR = "sard-agent_%s_linux_arm64.tar.gz"
    const val SIZE = 1000

    fun files(version: String) = setOf(DEB.format(version), TAR.format(version))

    fun content(seed: Int) = ByteArray(SIZE) { (it * seed).toByte() }

    fun sha256(file: Path): String = MessageDigest.getInstance("SHA-256").digest(file.readBytes()).toHexString()

    /** Writes the release of [version] into [dir] and returns [dir]. */
    fun write(
        dir: Path,
        version: String,
    ): Path {
        dir.createDirectories()
        val packages =
            listOf(Package(DEB, "deb", "amd64", 7), Package(TAR, "tar.gz", "arm64", 13)).map {
                val file = dir.resolve(it.nameFormat.format(version))
                file.writeBytes(content(it.seed))
                file to it
            }
        val artifacts =
            packages.joinToString(",\n") { (file, pkg) ->
                """    {"file": "${file.fileName}", "os": "linux", """ +
                    """"arch": "${pkg.arch}", "format": "${pkg.format}", """ +
                    """"size": $SIZE, "sha256": "${sha256(file)}"}"""
            }
        dir.resolve("manifest.json").writeText(
            """{
  "schema": 1,
  "version": "$version",
  "package_version": "1.2.3",
  "commit": "0123456789abcdef",
  "restic_version": "0.19.1",
  "protocol_version": 1,
  "artifacts": [
$artifacts
  ]
}
""",
        )
        val summed = packages.map { it.first } + listOf(dir.resolve("manifest.json"))
        val sums = summed.joinToString("") { "${sha256(it)}  ${it.fileName}\n" }
        dir.resolve("SHA256SUMS").writeText(sums)
        return dir
    }

    private data class Package(
        val nameFormat: String,
        val format: String,
        val arch: String,
        val seed: Int,
    )

    fun tempRelease(version: String): Path = write(Files.createTempDirectory("sard-agent-packages"), version)
}
