// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.PkiFixtures.AGENT
import dev.sard.server.pki.PkiFixtures.CLOCK
import dev.sard.server.pki.PkiFixtures.NOW
import dev.sard.server.pki.PkiFixtures.random
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.bouncycastle.asn1.x509.SubjectPublicKeyInfo
import java.math.BigInteger
import java.security.cert.X509Certificate
import kotlin.test.Test
import kotlin.test.assertEquals

private const val SOURCE_URI = "sard://roles/source"
private const val VERIFIER_URI = "sard://roles/verifier"

@MutFlowTest
class AgentRoleUriTest {
    private val ca = CaKeyPair.generate(CLOCK, random())
    private val keys = Keys.generate(random())
    private val forged = ForgedCertificates(ca)

    private fun issued(role: AgentRole): X509Certificate {
        val key = SubjectPublicKeyInfo.getInstance(keys.public.encoded)
        return MutFlow.underTest { Certificates.agent(key, AGENT, role, ca, NOW, random()) }
    }

    private fun read(role: ForgedRole): CertificateRole {
        val certificate = forged.forge(keys, BigInteger.TEN, AGENT, role, NOW)
        return MutFlow.underTest { AgentRoleUri.read(certificate) }
    }

    private fun uris(vararg uris: String) = ForgedRole.Uris(uris.toList())

    private fun sanUris(certificate: X509Certificate): List<Pair<Int, String>> {
        val names = certificate.subjectAlternativeNames
        return names.map { (it[0] as Int) to it[1] as String }
    }

    @Test
    fun `Роль записывается вторым URI в SAN`() {
        for ((role, uri) in listOf(AgentRole.SOURCE to SOURCE_URI, AgentRole.VERIFIER to VERIFIER_URI)) {
            val names = sanUris(issued(role))
            assertEquals(2, names.size)
            assertEquals(setOf(6 to AGENT.uri(), 6 to uri), names.toSet())
        }
    }

    @Test
    fun `the role of an issued certificate reads back`() {
        assertEquals(CertificateRole.Valid(AgentRole.SOURCE), AgentRoleUri.read(issued(AgentRole.SOURCE)))
        assertEquals(CertificateRole.Valid(AgentRole.VERIFIER), AgentRoleUri.read(issued(AgentRole.VERIFIER)))
    }

    @Test
    fun `a certificate without a role URI has no role`() {
        assertEquals(CertificateRole.Missing, read(ForgedRole.Absent))
        for (other in listOf("sard://role/source", "https://roles/source", "sard://tenants/x/agents/y", "sard:roles")) {
            assertEquals(CertificateRole.Missing, read(uris(other)), other)
        }
    }

    @Test
    fun `only exactly the two role strings are a role`() {
        assertEquals(CertificateRole.Valid(AgentRole.VERIFIER), read(ForgedRole.Text("verifier")))
        assertEquals(CertificateRole.Valid(AgentRole.SOURCE), read(ForgedRole.Text("source")))
        for (uri in INEXACT_ROLE_URIS) {
            assertEquals(CertificateRole.Invalid, read(uris(uri)), uri)
        }
    }

    @Test
    fun `more than one role URI is invalid even when equal`() {
        assertEquals(CertificateRole.Invalid, read(uris(SOURCE_URI, SOURCE_URI)))
        assertEquals(CertificateRole.Invalid, read(uris(SOURCE_URI, VERIFIER_URI)))
    }

    @Test
    fun `the order of the names does not matter`() {
        assertEquals(CertificateRole.Valid(AgentRole.SOURCE), read(ForgedRole.Uris(listOf(SOURCE_URI), first = true)))
        assertEquals(CertificateRole.Valid(AgentRole.SOURCE), read(ForgedRole.Uris(listOf(SOURCE_URI))))
    }

    @Test
    fun `the identity is found wherever it stands among the names`() {
        val leaf = forged.forge(keys, BigInteger.TEN, AGENT, ForgedRole.Uris(listOf(SOURCE_URI), first = true), NOW)
        assertEquals(AGENT, MutFlow.underTest { AgentIdentity.of(leaf) })
    }

    @Test
    fun `the wire form of a role is its stored name and parses back`() {
        assertEquals("source", AgentRole.SOURCE.stored)
        assertEquals("verifier", AgentRole.VERIFIER.stored)
        assertEquals(AgentRole.VERIFIER, AgentRole.of("verifier"))
        assertEquals(null, AgentRole.parse("Verifier"))
        assertEquals(AgentRole.SOURCE, AgentRole.parse("source"))
    }
}
