// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.downloads

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull

private const val DEB = "sard-agent_1.2.3_amd64.deb"
private const val TAR = "sard-agent_v1.2.3_linux_arm64.tar.gz"
private val ARTIFACTS = listOf(AgentArtifact(DEB, 10, "d".repeat(64)), AgentArtifact(TAR, 20, "a".repeat(64)))
private val METADATA = mapOf(AgentPackageCatalog.MANIFEST to "m".repeat(64), AgentPackageCatalog.SUMS to "s".repeat(64))

private fun manifest(
    version: String = "v1.2.3",
    schema: Int = 1,
    artifacts: List<AgentArtifact> = ARTIFACTS,
) = AgentManifest(schema, version, artifacts)

@MutFlowTest
class AgentPackageCatalogTest {
    @Test
    fun `packages of the server's version are served with their sums as ETags`() {
        val catalog = MutFlow.underTest { AgentPackageCatalog.of(manifest(), "v1.2.3", METADATA) }
        assertEquals("v1.2.3", catalog.version)
        assertEquals(setOf(DEB, TAR, "manifest.json", "SHA256SUMS"), catalog.files)
        assertEquals("d".repeat(64), catalog.etag(DEB))
        assertEquals("s".repeat(64), catalog.etag("SHA256SUMS"))
        assertNull(catalog.etag("other.deb"))
    }

    @Test
    fun `the signature is served when the release has one`() {
        val signed = METADATA + (AgentPackageCatalog.SIGNATURE to "g".repeat(64))
        val catalog = MutFlow.underTest { AgentPackageCatalog.of(manifest(), "v1.2.3", signed) }
        assertEquals("g".repeat(64), catalog.etag("SHA256SUMS.minisig"))
    }

    @Test
    fun `packages of another version stop the server`() {
        val e =
            assertFailsWith<AgentPackagesUnavailable> {
                MutFlow.underTest { AgentPackageCatalog.of(manifest(version = "v1.2.2"), "v1.2.3", METADATA) }
            }
        assertContains(e.message!!, "agent packages are version v1.2.2, the server is v1.2.3")
    }

    @Test
    fun `an unknown manifest schema is refused`() {
        val e =
            assertFailsWith<AgentPackagesUnavailable> {
                MutFlow.underTest { AgentPackageCatalog.of(manifest(schema = 2), "v1.2.3", METADATA) }
            }
        assertContains(e.message!!, "schema 2")
    }

    @Test
    fun `a manifest without artifacts is refused`() {
        val e =
            assertFailsWith<AgentPackagesUnavailable> {
                MutFlow.underTest { AgentPackageCatalog.of(manifest(artifacts = emptyList()), "v1.2.3", METADATA) }
            }
        assertContains(e.message!!, "no artifacts")
    }

    @Test
    fun `a file name that is not a plain name is refused`() {
        for (name in listOf("../etc/passwd", "a/b.deb", "", ".hidden", "a b.deb")) {
            val bad = manifest(artifacts = listOf(AgentArtifact(name, 1, "d".repeat(64))))
            val e =
                assertFailsWith<AgentPackagesUnavailable>(name) {
                    MutFlow.underTest { AgentPackageCatalog.of(bad, "v1.2.3", METADATA) }
                }
            assertContains(e.message!!, "file name")
        }
    }

    @Test
    fun `the manifest and SHA256SUMS are required`() {
        for (missing in listOf(AgentPackageCatalog.MANIFEST, AgentPackageCatalog.SUMS)) {
            val e =
                assertFailsWith<AgentPackagesUnavailable>(missing) {
                    MutFlow.underTest { AgentPackageCatalog.of(manifest(), "v1.2.3", METADATA - missing) }
                }
            assertContains(e.message!!, missing)
        }
    }
}
