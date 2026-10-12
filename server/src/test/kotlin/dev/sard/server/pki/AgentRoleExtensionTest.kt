// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.PkiFixtures.AGENT
import dev.sard.server.pki.PkiFixtures.CLOCK
import dev.sard.server.pki.PkiFixtures.NOW
import dev.sard.server.pki.PkiFixtures.random
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.bouncycastle.asn1.ASN1OctetString
import org.bouncycastle.asn1.ASN1Primitive
import org.bouncycastle.asn1.ASN1UTF8String
import org.bouncycastle.asn1.x509.SubjectPublicKeyInfo
import org.bouncycastle.cert.jcajce.JcaX509CertificateHolder
import java.math.BigInteger
import java.security.cert.X509Certificate
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** The OID the spec fixes: UUID 378b97d4-9756-4764-8ea0-056286ce38b8 under 2.25. */
private const val ROLE_OID = "2.25.73832348518563859722830588044060276920"

@MutFlowTest
class AgentRoleExtensionTest {
    private val ca = CaKeyPair.generate(CLOCK, random())
    private val keys = Keys.generate(random())
    private val forged = ForgedCertificates(ca)

    private fun issued(role: AgentRole): X509Certificate {
        val key = SubjectPublicKeyInfo.getInstance(keys.public.encoded)
        return MutFlow.underTest { Certificates.agent(key, AGENT, role, ca, NOW, random()) }
    }

    private fun forge(role: ForgedRole) = forged.forge(keys, BigInteger.TEN, AGENT, role, NOW)

    private fun read(role: ForgedRole) = MutFlow.underTest { AgentRoleExtension.read(forge(role)) }

    @Test
    fun `the OID is the fixed one`() {
        assertEquals(ROLE_OID, AgentRoleExtension.OID)
    }

    @Test
    fun `the role extension encodes the role as a DER UTF8String and is not critical`() {
        for ((role, text) in listOf(AgentRole.SOURCE to "source", AgentRole.VERIFIER to "verifier")) {
            val cert = issued(role)
            val wrapped = ASN1OctetString.getInstance(cert.getExtensionValue(ROLE_OID)).octets
            assertEquals(text, ASN1UTF8String.getInstance(ASN1Primitive.fromByteArray(wrapped)).string)
            assertTrue(ROLE_OID in cert.nonCriticalExtensionOIDs)
            assertFalse(ROLE_OID in cert.criticalExtensionOIDs)
            val holder = JcaX509CertificateHolder(cert)
            assertEquals(1, holder.extensions.extensionOIDs.count { it.id == ROLE_OID })
        }
    }

    @Test
    fun `the role of an issued certificate reads back`() {
        assertEquals(CertificateRole.Valid(AgentRole.SOURCE), AgentRoleExtension.read(issued(AgentRole.SOURCE)))
        assertEquals(CertificateRole.Valid(AgentRole.VERIFIER), AgentRoleExtension.read(issued(AgentRole.VERIFIER)))
    }

    @Test
    fun `a certificate without the extension has no role`() {
        assertEquals(CertificateRole.Missing, read(ForgedRole.Absent))
    }

    @Test
    fun `only exactly source or verifier in a UTF8String is a role`() {
        assertEquals(CertificateRole.Valid(AgentRole.VERIFIER), read(ForgedRole.Text("verifier")))
        assertEquals(CertificateRole.Valid(AgentRole.SOURCE), read(ForgedRole.Text("source")))
        for (text in listOf("admin", "Source", "SOURCE", "", " source", "source\n")) {
            assertEquals(CertificateRole.Invalid, read(ForgedRole.Text(text)), "'$text'")
        }
    }

    @Test
    fun `a PrintableString or bytes that are not DER is an invalid role`() {
        assertEquals(CertificateRole.Invalid, read(ForgedRole.Printable("source")))
        assertEquals(CertificateRole.Invalid, read(ForgedRole.Raw(byteArrayOf(0x0c, 0x7f, 0x01))))
        assertEquals(CertificateRole.Invalid, read(ForgedRole.Raw(ByteArray(0))))
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
