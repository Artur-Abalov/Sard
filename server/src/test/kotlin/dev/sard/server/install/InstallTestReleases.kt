// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

import dev.sard.server.downloads.AgentArtifact
import dev.sard.server.downloads.AgentManifest
import dev.sard.server.downloads.AgentOffer
import dev.sard.server.downloads.AgentPackageCatalog
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Primary

private const val SHA = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"

/** The release of the specification: v1.4.0, restic 0.19.1, packages named as make package names them. */
fun release(
    version: String = "v1.4.0",
    signed: Boolean = true,
): AgentOffer.Serving {
    val packageVersion = version.removePrefix("v")
    val artifacts =
        listOf("amd64", "arm64").flatMap { arch ->
            listOf(
                AgentArtifact("sard-agent_${version}_linux_$arch.tar.gz", 1000, SHA, arch, "tar.gz"),
                AgentArtifact("sard-agent_${packageVersion}_$arch.deb", 1000, SHA, arch, "deb"),
                AgentArtifact("sard-agent-$packageVersion-1.$arch.rpm", 1000, SHA, arch, "rpm"),
            )
        }
    val metadata = mapOf("manifest.json" to SHA, "SHA256SUMS" to SHA)
    val signature = if (signed) mapOf("SHA256SUMS.minisig" to SHA) else emptyMap()
    val manifest = AgentManifest(1, version, "0.19.1", artifacts)
    return AgentOffer.Serving(AgentPackageCatalog.of(manifest, version, metadata + signature))
}

/** A server that serves the signed release v1.4.0 (the real image of a release does). */
@TestConfiguration(proxyBeanMethods = false)
class SignedRelease {
    @Bean
    @Primary
    fun signedRelease(): AgentOffer = release()
}

/** A server that serves v1.4.0 without SHA256SUMS.minisig (an image built without a release). */
@TestConfiguration(proxyBeanMethods = false)
class UnsignedRelease {
    @Bean
    @Primary
    fun unsignedRelease(): AgentOffer = release(signed = false)
}

/** A server of version v1.4.0 with downloads off. */
@TestConfiguration(proxyBeanMethods = false)
class NoDownloads {
    @Bean
    @Primary
    fun noDownloads(): AgentOffer = AgentOffer.Withheld("v1.4.0")
}
