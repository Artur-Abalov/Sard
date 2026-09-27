// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.PkiFixtures.AGENT
import dev.sard.server.pki.PkiFixtures.CLOCK
import dev.sard.server.pki.PkiFixtures.NOW
import dev.sard.server.pki.PkiFixtures.SERVER_NAMES
import dev.sard.server.pki.PkiFixtures.certificate
import dev.sard.server.pki.PkiFixtures.certificates
import dev.sard.server.pki.PkiFixtures.random
import dev.sard.server.pki.PkiFixtures.resource
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.PosixFilePermissions
import java.security.cert.X509Certificate
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val CLIENT_AUTH = "1.3.6.1.5.5.7.3.2"
private const val SERVER_AUTH = "1.3.6.1.5.5.7.3.1"
private const val SAN_DNS = 2
private const val SAN_URI = 6
private const val SAN_IP = 7
private const val DIGITAL_SIGNATURE = 0
private const val KEY_CERT_SIGN = 5

@MutFlowTest
class FileCertificateAuthorityTest {
    @TempDir
    lateinit var tmp: Path

    private val dir get() = tmp.resolve("pki")

    private fun ca() = FileCertificateAuthority(dir, SERVER_NAMES, CLOCK, random())

    private fun caCertificate(ca: CertificateAuthority) = certificate(ca.caBundlePem())

    private fun perms(path: Path) = PosixFilePermissions.toString(Files.getPosixFilePermissions(path))

    // --- first start and restart

    @Test
    fun `the first start writes the CA with the key readable by the owner only`() {
        ca()
        assertEquals("rwx------", perms(dir))
        assertEquals("rw-------", perms(dir.resolve("ca/ca.key")))
        assertTrue(Files.exists(dir.resolve("ca/ca.crt")))
        assertEquals(listOf("ca"), Files.list(dir).map { it.fileName.toString() }.toList())
    }

    @Test
    fun `a restart loads the same CA`() {
        val first = ca()
        val second = ca()
        assertEquals(first.fingerprint(), second.fingerprint())
        assertEquals(first.caBundlePem(), second.caBundlePem())
    }

    @Test
    fun `the fingerprint is taken from the CA in the bundle`() {
        val ca = ca()
        assertEquals(CaFingerprint.of(caCertificate(ca)), MutFlow.underTest { ca.fingerprint() })
    }

    @Test
    fun `the CA key never shows in toString`() {
        val ca = ca()
        val key = String(Files.readAllBytes(dir.resolve("ca/ca.key")))
        val body = key.lines().filterNot { it.startsWith("-----") }.joinToString("")
        assertFalse(body in ca.toString())
        assertFalse("PRIVATE" in ca.toString())
    }

    // --- agent certificates from Go CSRs

    private fun issue(csr: String): X509Certificate {
        val ca = ca()
        val issued = MutFlow.underTest { ca.issueAgentCertificate(resource(csr), AGENT) }
        val chain = certificates(issued.chainPem)
        val leaf = chain.first()
        leaf.verify(caCertificate(ca).publicKey)
        assertEquals(leaf.serialNumber, issued.serial)
        assertEquals(leaf.notAfter.toInstant(), issued.notAfter)
        return leaf
    }

    @Test
    fun `a Go P-256 CSR yields a one-year client certificate naming the tenant and the agent`() {
        val leaf = issue("agent-p256.csr")
        assertEquals(listOf(CLIENT_AUTH), leaf.extendedKeyUsage)
        val uri = "sard://tenants/${AGENT.tenantId}/agents/${AGENT.agentId}"
        assertEquals(listOf(listOf<Any>(SAN_URI, uri)), leaf.subjectAlternativeNames.map { it.toList() })
        assertEquals("CN=${AGENT.agentId}", leaf.subjectX500Principal.name)
        assertEquals(NOW, leaf.notBefore.toInstant())
        assertEquals(NOW + Duration.ofDays(365), leaf.notAfter.toInstant())
        assertEquals(-1, leaf.basicConstraints, "not a CA")
        assertEquals(listOf(DIGITAL_SIGNATURE), leaf.keyUsage.indices.filter { leaf.keyUsage[it] })
    }

    @Test
    fun `the serial number is random, positive and 128 bits long`() {
        val a = issue("agent-p256.csr").serialNumber
        val b = issue("agent-p256.csr").serialNumber
        assertEquals(listOf(1, 128), listOf(a.signum(), a.bitLength()), "serial $a")
        assertTrue(a != b)
    }

    @Test
    fun `a Go P-384 CSR is accepted`() {
        assertEquals(listOf(CLIENT_AUTH), issue("agent-p384.csr").extendedKeyUsage)
    }

    @Test
    fun `a CSR whose signature does not verify is refused`() {
        assertFailsWith<InvalidCsrException> { issue("agent-bad-signature.csr") }
    }

    @Test
    fun `RSA, Ed25519 and ECDSA keys on other curves are refused`() {
        assertFailsWith<InvalidCsrException> { issue("agent-rsa.csr") }
        assertFailsWith<InvalidCsrException> { issue("agent-ed25519.csr") }
        assertFailsWith<InvalidCsrException> { issue("agent-p521.csr") }
    }

    @Test
    fun `bytes that are not a CSR are refused`() {
        val ca = ca()
        val notCsr = byteArrayOf(1, 2, 3)
        assertFailsWith<InvalidCsrException> { MutFlow.underTest { ca.issueAgentCertificate(notCsr, AGENT) } }
    }

    // --- server certificate

    /** The server profile: signed by the CA, serverAuth only, the configured names, 90 days. */
    private fun assertServerCertificate(
        ca: CertificateAuthority,
        leaf: X509Certificate,
        issuedAt: java.time.Instant,
    ) {
        leaf.verify(caCertificate(ca).publicKey)
        assertEquals(listOf(SERVER_AUTH), leaf.extendedKeyUsage)
        val sans = leaf.subjectAlternativeNames.map { it.toList() }
        val expected =
            listOf(listOf<Any>(SAN_DNS, "localhost"), listOf(SAN_IP, "127.0.0.1"), listOf(SAN_IP, "0:0:0:0:0:0:0:1"))
        assertEquals(expected, sans)
        assertEquals(issuedAt - Duration.ofHours(1), leaf.notBefore.toInstant())
        assertEquals(issuedAt + Duration.ofDays(90), leaf.notAfter.toInstant())
    }

    @Test
    fun `the server certificate is a 90-day server certificate for the configured names`() {
        val ca = ca()
        val km = MutFlow.underTest { ca.serverKeyManager() }
        val alias = km.chooseServerAlias("EC", null, null)
        assertServerCertificate(ca, km.getCertificateChain(alias).first(), NOW)
        assertEquals(
            km
                .getCertificateChain(alias)
                .first()
                .publicKey.algorithm,
            km.getPrivateKey(alias).algorithm,
        )
        assertEquals(listOf(alias), km.getServerAliases("EC", null).toList())
    }

    @Test
    fun `the served chain ends with the CA so a new agent can check the pinned fingerprint`() {
        val ca = ca()
        val km = MutFlow.underTest { ca.serverKeyManager() }
        val chain = km.getCertificateChain(km.chooseServerAlias("EC", null, null)).toList()
        assertEquals(2, chain.size)
        assertEquals(caCertificate(ca), chain.last())
        assertEquals(ca.fingerprint(), CaFingerprint.of(chain.last()))
    }

    @Test
    fun `the server key is offered only for its own key type and never for clients`() {
        val km = ca().serverKeyManager()
        assertNull(MutFlow.underTest { km.chooseServerAlias("RSA", null, null) })
        assertNull(km.getServerAliases("RSA", null))
        assertNull(km.chooseClientAlias(arrayOf("EC"), null, null))
        assertNull(km.getClientAliases("EC", null))
        assertNull(km.getCertificateChain("unknown"))
        assertNull(km.getPrivateKey("unknown"))
    }

    @Test
    fun `the server certificate is renewed once 30 days or less remain, and not before`() {
        val clock = MovableClock(NOW)
        val ca = FileCertificateAuthority(dir, SERVER_NAMES, clock, random())
        val km = ca.serverKeyManager()
        val first = km.chooseServerAlias("EC", null, null)

        clock.now = NOW + Duration.ofDays(60) - Duration.ofSeconds(1)
        assertFalse(MutFlow.underTest { ca.renewServerCertificate() })
        assertEquals(first, km.chooseServerAlias("EC", null, null))

        clock.now = NOW + Duration.ofDays(60)
        assertTrue(MutFlow.underTest { ca.renewServerCertificate() })
        val second = km.chooseServerAlias("EC", null, null)
        assertTrue(first != second)
        assertServerCertificate(ca, km.getCertificateChain(second).first(), clock.now)
        assertTrue(km.getCertificateChain(first) != null, "a handshake that chose the old key can still finish")
        assertFalse(ca.renewServerCertificate(), "the fresh certificate is not due")
    }

    @Test
    fun `an IP server name is encoded from the bytes ServerNames parsed`() {
        // BouncyCastle's own GeneralName string constructor rejects a trailing "::" group
        // ("1:2:3:4:5:6:7::"); building the SAN from ServerNames' own bytes sidesteps it.
        val names = listOf("1:2:3:4:5:6:7::")
        val ca = FileCertificateAuthority(dir, names, CLOCK, random())
        val km = ca.serverKeyManager()
        val leaf = km.getCertificateChain(km.chooseServerAlias("EC", null, null)).first()
        val sans = leaf.subjectAlternativeNames.map { it.toList() }
        assertEquals(listOf(listOf<Any>(SAN_IP, "1:2:3:4:5:6:7:0")), sans)
    }

    @Test
    fun `a server name that is neither an IP literal nor a hostname stops the CA`() {
        for (name in listOf("999.1.1.1", "fe80::1%eth0", "bad name")) {
            assertFailsWith<IllegalArgumentException>(name) {
                FileCertificateAuthority(dir, listOf(name), CLOCK, random())
            }
        }
    }

    @Test
    fun `the server key stays in memory`() {
        ca()
        assertEquals(
            listOf("ca.crt", "ca.key"),
            Files
                .list(dir.resolve("ca"))
                .map { it.fileName.toString() }
                .sorted()
                .toList(),
        )
    }
}
