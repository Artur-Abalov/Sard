// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.downloads

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.http.MediaType
import kotlin.test.Test
import kotlin.test.assertEquals

@MutFlowTest
class AgentPackageHeadersTest {
    @Test
    fun `files named by their version are cached for good`() {
        val value = MutFlow.underTest { AgentPackageHeaders.cacheControl("sard-agent_1.2.3_amd64.deb") }
        assertEquals("public, max-age=31536000, immutable", value)
    }

    @Test
    fun `the manifest, sums and signature are revalidated every time`() {
        for (name in listOf("manifest.json", "SHA256SUMS", "SHA256SUMS.minisig")) {
            assertEquals("no-cache", MutFlow.underTest { AgentPackageHeaders.cacheControl(name) }, name)
        }
    }

    @Test
    fun `media types follow the package format`() {
        val expected =
            mapOf(
                "sard-agent_1.2.3_amd64.deb" to "application/vnd.debian.binary-package",
                "sard-agent-1.2.3-1.x86_64.rpm" to "application/x-rpm",
                "sard-agent_v1.2.3_linux_amd64.tar.gz" to "application/gzip",
                "manifest.json" to "application/json",
                "SHA256SUMS" to "text/plain;charset=UTF-8",
                "SHA256SUMS.minisig" to "text/plain;charset=UTF-8",
                "something.else" to "application/octet-stream",
            )
        for ((name, type) in expected) {
            val actual = MutFlow.underTest { AgentPackageHeaders.mediaType(name) }
            assertEquals(MediaType.parseMediaType(type), actual, name)
        }
    }
}
