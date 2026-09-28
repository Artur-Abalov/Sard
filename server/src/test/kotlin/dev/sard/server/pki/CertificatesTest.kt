// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.PkiFixtures.CLOCK
import dev.sard.server.pki.PkiFixtures.NOW
import dev.sard.server.pki.PkiFixtures.random
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.bouncycastle.asn1.x509.SubjectPublicKeyInfo
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val KEY_CERT_SIGN = 5
private const val CRL_SIGN = 6
private const val SAN_DNS = 2
private const val SAN_IP = 7
private const val SERVER_AUTH = "1.3.6.1.5.5.7.3.1"

@MutFlowTest
class CertificatesTest {
    @Test
    fun `the root is a self-signed ten-year CA able to sign an intermediate`() {
        val keys = Keys.generate(random())
        val root = MutFlow.underTest { Certificates.root(keys, NOW, random()) }
        root.verify(keys.public)
        assertEquals("CN=Sard CA", root.subjectX500Principal.name)
        assertEquals(Int.MAX_VALUE, root.basicConstraints, "CA without a path length limit")
        assertEquals(listOf(KEY_CERT_SIGN, CRL_SIGN), root.keyUsage.indices.filter { root.keyUsage[it] })
        assertEquals(NOW - Duration.ofHours(1), root.notBefore.toInstant())
        assertEquals(NOW + Duration.ofDays(3650), root.notAfter.toInstant())
        assertEquals(128, root.serialNumber.bitLength())
        assertTrue(root.subjectX500Principal == root.issuerX500Principal)
    }

    @Test
    fun `every server name is encoded as the IP SAN ServerNames parsed, in shapes a single name never covers`() {
        // Each name below forces a different arm of the manual IPv6/IPv4 parsers that a narrower
        // set of shapes leaves unexercised: a "::"-compressed literal with exactly one group to
        // fill, a "::"-compressed literal with more than one group to fill on both sides of the
        // "::", a fully uncompressed literal with a group above 0xff (so a high byte lands away
        // from a low byte), a "::" immediately before an embedded dotted IPv4, a fully uncompressed
        // literal with an embedded dotted IPv4 (no "::" at all), the maximum IPv4 octet, an
        // out-of-range IPv4 octet, and a "::" that leaves no group to fill (malformed, not a
        // redundant no-op) — the last two must fall back to a DNS SAN, not a wrong IP SAN.
        val names =
            listOf(
                "1:2:3:4:5:6:7::",
                "1:2::7:8",
                "ff00:1:2:3:4:5:6:7",
                "::1.2.3.4",
                "1:2:3:4:5:6:1.2.3.4",
                "255.255.255.255",
                "256.0.0.1",
                "1:2:3:4:5:6:7::8",
            )
        val ca = CaKeyPair.generate(CLOCK, random())
        val key = SubjectPublicKeyInfo.getInstance(Keys.generate(random()).public.encoded)
        val cert = MutFlow.underTest { Certificates.server(key, names, ca, NOW, random()) }
        assertEquals(listOf(SERVER_AUTH), cert.extendedKeyUsage)
        assertEquals(NOW - Duration.ofHours(1), cert.notBefore.toInstant())
        assertEquals(NOW + Duration.ofDays(90), cert.notAfter.toInstant())
        val sans = cert.subjectAlternativeNames.map { it.toList() }
        assertEquals(
            listOf(
                listOf<Any>(SAN_IP, "1:2:3:4:5:6:7:0"),
                listOf<Any>(SAN_IP, "1:2:0:0:0:0:7:8"),
                listOf<Any>(SAN_IP, "ff00:1:2:3:4:5:6:7"),
                listOf<Any>(SAN_IP, "0:0:0:0:0:0:102:304"),
                listOf<Any>(SAN_IP, "1:2:3:4:5:6:102:304"),
                listOf<Any>(SAN_IP, "255.255.255.255"),
                listOf<Any>(SAN_DNS, "256.0.0.1"),
                listOf<Any>(SAN_DNS, "1:2:3:4:5:6:7::8"),
            ),
            sans,
        )
    }
}
