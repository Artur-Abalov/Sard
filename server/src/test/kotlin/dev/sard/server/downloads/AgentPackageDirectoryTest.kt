// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.downloads

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.io.TempDir
import tools.jackson.databind.json.JsonMapper
import java.nio.file.Path
import kotlin.io.path.createDirectories
import kotlin.io.path.deleteExisting
import kotlin.io.path.readText
import kotlin.io.path.writeBytes
import kotlin.io.path.writeText
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull

private val mapper = JsonMapper.builder().build()

@MutFlowTest
class AgentPackageDirectoryTest {
    @TempDir lateinit var tmp: Path

    private val dir: Path by lazy { AgentPackageFixture.write(tmp.resolve("packages"), "v1.2.3") }

    private fun load(at: Path = dir) = AgentPackageDirectory.load(at, "v1.2.3", mapper)

    private fun refusal(at: Path = dir): String {
        val refused = assertFailsWith<AgentPackagesUnavailable> { MutFlow.underTest { load(at) } }
        return refused.message!!
    }

    @Test
    fun `a release made by make package is served`() {
        val catalog = MutFlow.underTest { load() }
        assertEquals("v1.2.3", catalog.version)
        assertEquals(AgentPackageFixture.files("v1.2.3") + setOf("manifest.json", "SHA256SUMS"), catalog.files)
        assertEquals(AgentPackageFixture.sha256(dir.resolve("SHA256SUMS")), catalog.etag("SHA256SUMS"))
        assertEquals(AgentPackageFixture.sha256(dir.resolve("manifest.json")), catalog.etag("manifest.json"))
        assertNull(catalog.etag("SHA256SUMS.minisig"))
    }

    @Test
    fun `a signed release serves its signature`() {
        dir.resolve("SHA256SUMS.minisig").writeText("untrusted comment: signature\n")
        val catalog = MutFlow.underTest { load() }
        assertEquals(AgentPackageFixture.sha256(dir.resolve("SHA256SUMS.minisig")), catalog.etag("SHA256SUMS.minisig"))
    }

    @Test
    fun `no directory stops the server and says how to switch downloads off`() {
        val message = refusal(tmp.resolve("absent"))
        assertContains(message, tmp.resolve("absent").toString())
        assertContains(message, "SARD_AGENT_DOWNLOADS=false")
    }

    @Test
    fun `no manifest stops the server`() {
        dir.resolve("manifest.json").deleteExisting()
        assertContains(refusal(), "manifest.json")
    }

    @Test
    fun `a manifest that is not JSON stops the server`() {
        dir.resolve("manifest.json").writeText("{ not json")
        assertContains(refusal(), "manifest.json is not a valid manifest")
    }

    @Test
    fun `a manifest naming a file outside the directory stops the server`() {
        val manifest = dir.resolve("manifest.json")
        manifest.writeText(manifest.readText().replace("sard-agent_v1.2.3_amd64.deb", "../../etc/passwd"))
        assertContains(refusal(), "\"../../etc/passwd\" is not a plain file name")
    }

    @Test
    fun `no SHA256SUMS stops the server`() {
        dir.resolve("SHA256SUMS").deleteExisting()
        assertContains(refusal(), "SHA256SUMS")
    }

    @Test
    fun `a listed package that is missing stops the server`() {
        dir.resolve(AgentPackageFixture.DEB.format("v1.2.3")).deleteExisting()
        assertContains(refusal(), "sard-agent_v1.2.3_amd64.deb is missing")
    }

    @Test
    fun `a package of another size stops the server`() {
        dir.resolve(AgentPackageFixture.DEB.format("v1.2.3")).writeBytes(ByteArray(3))
        assertContains(refusal(), "sard-agent_v1.2.3_amd64.deb is 3 bytes, the manifest says 1000")
    }

    @Test
    fun `packages of another version stop the server`() {
        AgentPackageFixture.write(tmp.resolve("old").createDirectories(), "v1.2.2")
        assertContains(refusal(tmp.resolve("old")), "agent packages are version v1.2.2, the server is v1.2.3")
    }
}
