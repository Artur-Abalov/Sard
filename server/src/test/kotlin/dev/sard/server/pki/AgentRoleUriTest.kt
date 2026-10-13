// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.PkiFixtures.AGENT
import dev.sard.server.pki.PkiFixtures.CLOCK
import dev.sard.server.pki.PkiFixtures.NOW
import dev.sard.server.pki.PkiFixtures.random
import dev.sard.server.role.AgentRole
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.bouncycastle.asn1.x509.SubjectPublicKeyInfo
import java.math.BigInteger
import java.security.cert.X509Certificate
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

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
    fun `an agent certificate is valid for a year from now, with a 128-bit positive serial`() {
        val certificate = issued(AgentRole.SOURCE)

        assertEquals(NOW, certificate.notBefore.toInstant())
        assertEquals(NOW.plus(Duration.ofDays(365)), certificate.notAfter.toInstant())
        assertEquals(128, certificate.serialNumber.bitLength())
    }

    @Test
    fun `the role of an issued certificate reads back`() {
        assertEquals(CertificateRole.Valid(AgentRole.SOURCE), AgentRoleUri.read(issued(AgentRole.SOURCE)))
        assertEquals(CertificateRole.Valid(AgentRole.VERIFIER), AgentRoleUri.read(issued(AgentRole.VERIFIER)))
    }

    @Test
    fun `a certificate without a role URI has no role`() {
        assertEquals(CertificateRole.Missing, read(ForgedRole.Absent))
    }

    @Test
    fun `the shared role URI vectors read as the file says`() {
        assertTrue(ROLE_URI_VECTORS.isNotEmpty())
        for ((uri, outcome) in ROLE_URI_VECTORS) {
            val expected =
                when (outcome) {
                    "missing" -> CertificateRole.Missing
                    "invalid" -> CertificateRole.Invalid
                    else -> CertificateRole.Valid(AgentRole.of(outcome))
                }
            assertEquals(expected, read(uris(uri)), uri)
        }
    }

    @Test
    fun `only exactly the two role strings are a role`() {
        assertEquals(CertificateRole.Valid(AgentRole.VERIFIER), read(ForgedRole.Text("verifier")))
        assertEquals(CertificateRole.Valid(AgentRole.SOURCE), read(ForgedRole.Text("source")))
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
