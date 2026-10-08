// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.CaImportFixtures.CLOCK
import dev.sard.server.pki.CaImportFixtures.NOW
import dev.sard.server.pki.PkiFixtures.random
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.extension.ExtendWith
import org.junit.jupiter.api.io.TempDir
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import java.nio.file.Path
import java.security.cert.X509Certificate
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

private val NAMES = listOf("sard.example.com", "localhost")

@MutFlowTest
@ExtendWith(OutputCaptureExtension::class)
class FileCertificateAuthorityImportTest {
    @TempDir
    lateinit var tmp: Path

    private val dir get() = tmp.resolve("pki")
    private val importDir get() = tmp.resolve("import")
    private val original = CaImportFixtures.original()

    private fun ca(
        clock: java.time.Clock = CLOCK,
        import: Path? = importDir,
    ) = MutFlow.underTest { FileCertificateAuthority(dir, NAMES, clock, random(), import) }

    private fun chain(ca: CertificateAuthority): List<X509Certificate> {
        val km = ca.serverKeyManager()
        return km.getCertificateChain(km.chooseServerAlias("EC", null, null)).toList()
    }

    @Test
    fun `a server that imported the CA serves certificates it signed and pins the original fingerprint`() {
        CaImportFixtures.source(importDir, original)
        val ca = ca()
        assertEquals(CaFingerprint.of(original.certificate), ca.fingerprint())
        val chain = chain(ca)
        assertEquals(original.certificate, chain.last())
        chain.first().verify(original.certificate.publicKey)
        assertEquals(PkiFixtures.certificates(ca.caBundlePem()), listOf(original.certificate))
    }

    @Test
    fun `the server certificate carries the names of the new configuration`() {
        CaImportFixtures.source(importDir, original)
        val names = chain(ca()).first().subjectAlternativeNames.map { it[1] }
        assertEquals(NAMES, names)
    }

    @Test
    fun `an agent certificate is signed by the imported CA`() {
        CaImportFixtures.source(importDir, original)
        val csr = PkiFixtures.resource("agent-p256.csr")
        val issued = ca().issueAgentCertificate(csr, PkiFixtures.AGENT)
        val leaf = PkiFixtures.certificates(issued.chainPem).first()
        leaf.verify(original.certificate.publicKey)
    }

    @Test
    fun `the renewal of the server certificate is signed by the imported CA`() {
        CaImportFixtures.source(importDir, original)
        val clock = MovableClock(NOW)
        val ca = ca(clock)
        clock.now = NOW + Duration.ofDays(60)
        assertTrue(ca.renewServerCertificate())
        chain(ca).first().verify(original.certificate.publicKey)
    }

    @Test
    fun `every start logs the fingerprint with where the CA came from`(output: CapturedOutput) {
        ca(import = null)
        val generated = output.all.lines().single { "origin=generated" in it }
        assertTrue("INFO" in generated && Regex("fingerprint=[0-9a-f]{64}").containsMatchIn(generated), generated)
        assertTrue("imported" !in generated, generated)
        ca(import = null)
        assertTrue(output.all.lines().any { "origin=existing" in it })
    }

    @Test
    fun `an import is logged at INFO with the source path and the fingerprint`(output: CapturedOutput) {
        CaImportFixtures.source(importDir, original)
        ca()
        val line = output.all.lines().single { "origin=imported" in it }
        val fingerprint = CaFingerprint.of(original.certificate).hex
        assertTrue("INFO" in line && importDir.toString() in line && "fingerprint=$fingerprint" in line, line)
        ca()
        assertTrue(output.all.lines().any { "origin=existing" in it && "fingerprint=$fingerprint" in it })
    }

    @Test
    fun `the key never reaches the log, on success or on refusal`(output: CapturedOutput) {
        CaImportFixtures.source(importDir, original)
        ca()
        val fragments = CaImportFixtures.keyFragments(Pem.privateKey(original.privateKey))
        for (fragment in fragments) assertTrue(fragment !in output.all, "key line in the log")
        CaImportFixtures.chmod(importDir.resolve("ca/ca.crt"), "rw-r--r--")
        val refused = assertFailsWith<CaImportRefused> { FileCertificateAuthority(tmp.resolve("other"), NAMES, CLOCK, random(), importDir) }
        for (fragment in fragments) assertTrue(fragment !in refused.message.orEmpty() && fragment !in output.all, "key line leaked")
    }

    @Test
    fun `a refused import stops the start with the reason and leaves the CA directory without a CA`() {
        CaImportFixtures.source(importDir, original)
        CaImportFixtures.chmod(importDir.resolve("ca/ca.key"), "rw-r-----")
        val e = assertFailsWith<CaImportRefused> { ca() }
        assertEquals(CaImportRefusal.IMPORT_PERMISSIONS_TOO_OPEN, e.reason)
        assertTrue(!java.nio.file.Files.exists(dir.resolve("ca")))
    }
}
