// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.PkiFixtures.AGENT
import dev.sard.server.pki.PkiFixtures.CLOCK
import dev.sard.server.pki.PkiFixtures.SERVER_NAMES
import dev.sard.server.pki.PkiFixtures.certificate
import dev.sard.server.pki.PkiFixtures.certificates
import dev.sard.server.pki.PkiFixtures.random
import dev.sard.server.pki.PkiFixtures.resource
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private const val URI =
    "sard://tenants/7f3c1a52-0b4e-4c1d-9a55-2d8e6f0b9c11" +
        "/agents/0c9d2b4e-5a61-4f7e-8b3a-1d2e3f4a5b6c"

/** ADR 0014: an agent certificate names its tenant and agent in one URI SAN. */
@MutFlowTest
class AgentIdentityTest {
    @TempDir
    lateinit var tmp: Path

    @Test
    fun `the URI names the tenant and the agent`() {
        assertEquals(URI, MutFlow.underTest { AGENT.uri() })
    }

    @Test
    fun `a well-formed URI parses back to the identity`() {
        assertEquals(AGENT, MutFlow.underTest { AgentIdentity.parse(URI) })
    }

    @Test
    fun `anything but the exact agent URI is no identity`() {
        val malformed =
            listOf(
                "",
                URI.replace("sard://", "https://"),
                URI.replace("/tenants/", "/tenant/"),
                URI.replace("/agents/", "/agent/"),
                "$URI/",
                "x$URI",
                URI.uppercase(),
                URI.replace("7f3c1a52", "7f3c1a5"),
                URI.replace("0c9d2b4e", "0c9d2b4g"),
            )
        for (uri in malformed) {
            assertNull(MutFlow.underTest { AgentIdentity.parse(uri) }, uri)
        }
    }

    @Test
    fun `an agent certificate carries its identity`() {
        val ca = FileCertificateAuthority(tmp.resolve("pki"), SERVER_NAMES, CLOCK, random())
        val issued = ca.issueAgentCertificate(resource("agent-p256.csr"), AGENT)
        val leaf = certificates(issued.chainPem).first()
        assertEquals(AGENT, MutFlow.underTest { AgentIdentity.of(leaf) })
    }

    @Test
    fun `a certificate without an agent URI carries none`() {
        val ca = FileCertificateAuthority(tmp.resolve("pki"), SERVER_NAMES, CLOCK, random())
        val root = certificate(ca.caBundlePem())
        assertNull(MutFlow.underTest { AgentIdentity.of(root) })
        val server = ca.serverKeyManager().let { it.getCertificateChain(it.getServerAliases("EC", null).first()) }
        assertNull(MutFlow.underTest { AgentIdentity.of(server.first()) })
    }
}
