// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.CaImportFixtures.CLOCK
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.bouncycastle.asn1.x509.KeyUsage
import org.junit.jupiter.api.extension.ExtendWith
import org.junit.jupiter.api.io.TempDir
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import java.nio.file.Files
import java.nio.file.Path
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

@MutFlowTest
@ExtendWith(OutputCaptureExtension::class)
class CaImportSourceTest {
    @TempDir
    lateinit var tmp: Path

    private val root get() = tmp.resolve("import")

    private fun read(at: Path = root): ImportedCa = MutFlow.underTest { CaImportSource(at, CLOCK).read() }

    private fun refusal(
        at: Path = root,
        reason: CaImportRefusal,
    ): String {
        val e = assertFailsWith<CaImportRefused> { read(at) }
        assertEquals(reason, e.reason)
        val message = e.message.orEmpty()
        assertTrue(message.startsWith("CA import refused"), message)
        assertTrue(reason.name in message, message)
        assertTrue("SARD_PKI_IMPORT_DIR" in message, message)
        return message
    }

    @Test
    fun `a source with the original CA is read as the same certificate and key`() {
        val original = CaImportFixtures.original()
        CaImportFixtures.source(root, original)
        val read = read()
        assertContentEquals(original.certificate.encoded, read.certificate.encoded)
        assertContentEquals(original.privateKey.encoded, read.key.encoded)
    }

    @Test
    fun `a path that does not exist or is a file is not a source`() {
        assertTrue(root.toString() in refusal(root, CaImportRefusal.IMPORT_SOURCE_MISSING))
        val file = Files.createFile(tmp.resolve("file"))
        assertTrue(file.toString() in refusal(file, CaImportRefusal.IMPORT_SOURCE_MISSING))
    }

    @Test
    fun `a missing ca directory, certificate or key names the expected path`() {
        Files.createDirectories(root)
        assertTrue(root.resolve("ca/ca.crt").toString() in refusal(reason = CaImportRefusal.IMPORT_FILE_MISSING))
        CaImportFixtures.source(root, CaImportFixtures.original())
        Files.delete(root.resolve("ca/ca.crt"))
        assertTrue(root.resolve("ca/ca.crt").toString() in refusal(reason = CaImportRefusal.IMPORT_FILE_MISSING))
        CaImportFixtures.source(root, CaImportFixtures.original())
        Files.delete(root.resolve("ca/ca.key"))
        assertTrue(root.resolve("ca/ca.key").toString() in refusal(reason = CaImportRefusal.IMPORT_FILE_MISSING))
    }

    @Test
    fun `files laid out without the ca directory are refused with the expected path`() {
        val original = CaImportFixtures.original()
        Files.createDirectories(root)
        Files.writeString(root.resolve("ca.crt"), Pem.certificate(original.certificate))
        Files.writeString(root.resolve("ca.key"), Pem.privateKey(original.privateKey))
        val message = refusal(reason = CaImportRefusal.IMPORT_FILE_MISSING)
        assertTrue(root.resolve("ca/ca.crt").toString() in message, message)
    }

    @Test
    fun `a file or directory the server cannot read is refused naming it`() {
        CaImportFixtures.source(root, CaImportFixtures.original())
        val key = root.resolve("ca/ca.key")
        val unreadable =
            assertFailsWith<CaImportRefused> {
                MutFlow.underTest { CaImportSource(root, CLOCK, isReadable = { it != key }).read() }
            }
        assertEquals(CaImportRefusal.IMPORT_FILE_UNREADABLE, unreadable.reason)
        assertTrue(key.toString() in unreadable.message.orEmpty(), unreadable.message)
        for (path in listOf(root, root.resolve("ca"), root.resolve("ca/ca.crt"))) {
            val e =
                assertFailsWith<CaImportRefused> {
                    MutFlow.underTest { CaImportSource(root, CLOCK, isReadable = { it != path }).read() }
                }
            assertEquals(CaImportRefusal.IMPORT_FILE_UNREADABLE, e.reason, path.toString())
            assertTrue(path.toString() in e.message.orEmpty(), e.message)
        }
    }

    @Test
    fun `permissions wider than the owner are refused naming the path and the mode`() {
        val cases =
            listOf(
                Triple("ca/ca.key", "rw-r-----", "rw-r-----"),
                Triple("ca/ca.key", "rw----r--", "rw----r--"),
                Triple("ca/ca.crt", "rw-r--r--", "rw-r--r--"),
                Triple("ca", "rwxr-x---", "rwxr-x---"),
                Triple(".", "rwxr-xr-x", "rwxr-xr-x"),
            )
        for ((relative, mode, shown) in cases) {
            CaImportFixtures.source(root, CaImportFixtures.original())
            val target = root.resolve(relative).normalize()
            CaImportFixtures.chmod(target, mode)
            val message = refusal(reason = CaImportRefusal.IMPORT_PERMISSIONS_TOO_OPEN)
            assertTrue(target.toString() in message && shown in message, "$relative $mode: $message")
        }
    }

    @Test
    fun `permissions are checked before the content of the files`() {
        CaImportFixtures.source(root, "not a certificate", "not a key")
        CaImportFixtures.chmod(root.resolve("ca/ca.key"), "rw-r--r--")
        refusal(reason = CaImportRefusal.IMPORT_PERMISSIONS_TOO_OPEN)
    }

    @Test
    fun `a key with mode 0400 is accepted`() {
        CaImportFixtures.source(root, CaImportFixtures.original())
        CaImportFixtures.chmod(root.resolve("ca/ca.key"), "r--------")
        read()
    }

    @Test
    fun `symbolic links are followed and the permissions of their targets count`() {
        val original = CaImportFixtures.original()
        val targets = CaImportFixtures.source(tmp.resolve("targets"), original)
        Files.createDirectories(root.resolve("ca"))
        Files.createSymbolicLink(root.resolve("ca/ca.crt"), targets.resolve("ca/ca.crt"))
        Files.createSymbolicLink(root.resolve("ca/ca.key"), targets.resolve("ca/ca.key"))
        CaImportFixtures.chmod(root, "rwx------")
        CaImportFixtures.chmod(root.resolve("ca"), "rwx------")
        assertContentEquals(original.certificate.encoded, read().certificate.encoded)
        CaImportFixtures.chmod(targets.resolve("ca/ca.key"), "rw-rw----")
        refusal(reason = CaImportRefusal.IMPORT_PERMISSIONS_TOO_OPEN)
    }

    private val original = CaImportFixtures.original()
    private val originalKeyPem get() = Pem.privateKey(original.privateKey)
    private val originalCertPem get() = Pem.certificate(original.certificate)

    private fun rejectsCertificate(text: String) {
        CaImportFixtures.source(root, text, originalKeyPem)
        val message = refusal(reason = CaImportRefusal.CA_CERT_INVALID)
        assertTrue(root.resolve("ca/ca.crt").toString() in message, message)
    }

    @Test
    fun `a certificate file that is empty, not PEM or holds two certificates is refused`() {
        rejectsCertificate("")
        rejectsCertificate("this is not a certificate\n")
        rejectsCertificate("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n")
        rejectsCertificate(originalCertPem + originalCertPem)
    }

    @Test
    fun `a key placed where the certificate belongs is refused without quoting it`() {
        CaImportFixtures.source(root, originalKeyPem, originalCertPem)
        val message = refusal(reason = CaImportRefusal.CA_CERT_INVALID)
        for (line in CaImportFixtures.keyFragments(originalKeyPem)) {
            assertTrue(line !in message, "key line in: $message")
        }
        assertTrue("PRIVATE KEY" !in message, message)
    }

    @Test
    fun `a key file that is empty, encrypted, SEC1 or garbled is refused naming PKCS 8 PEM`() {
        val lines = originalKeyPem.lines()
        val garbled = (listOf(lines[0], "AAAA" + lines[1].drop(4)) + lines.drop(2)).joinToString("\n")
        val keys =
            listOf(
                "",
                "-----BEGIN ENCRYPTED PRIVATE KEY-----\nAAAA\n-----END ENCRYPTED PRIVATE KEY-----\n",
                "-----BEGIN EC PRIVATE KEY-----\nAAAA\n-----END EC PRIVATE KEY-----\n",
                "-----BEGIN PRIVATE KEY-----\nnot base64!\n-----END PRIVATE KEY-----\n",
                garbled,
            )
        for (key in keys) {
            CaImportFixtures.source(root, originalCertPem, key)
            val message = refusal(reason = CaImportRefusal.CA_KEY_INVALID)
            assertTrue(root.resolve("ca/ca.key").toString() in message && "PKCS#8 PEM" in message, message)
        }
    }

    @Test
    fun `a key that is not ECDSA P-256 is refused naming the algorithm or curve`() {
        val rsa = CaImportFixtures.rsa()
        CaImportFixtures.source(
            root,
            Pem.certificate(CaImportFixtures.certificate(rsa)),
            Pem.privateKey(rsa.private),
        )
        assertTrue("RSA" in refusal(reason = CaImportRefusal.CA_KEY_UNSUPPORTED))
        val p384 = CaImportFixtures.p384()
        CaImportFixtures.source(
            root,
            Pem.certificate(CaImportFixtures.certificate(p384)),
            Pem.privateKey(p384.private),
        )
        assertTrue("P-384" in refusal(reason = CaImportRefusal.CA_KEY_UNSUPPORTED))
    }

    @Test
    fun `a key of another certificate is refused naming both files`() {
        CaImportFixtures.source(root, originalCertPem, Pem.privateKey(CaImportFixtures.p256().private))
        val message = refusal(reason = CaImportRefusal.CA_KEY_MISMATCH)
        val both = listOf("ca/ca.key", "ca/ca.crt").all { root.resolve(it).toString() in message }
        assertTrue(both, message)
    }

    private fun importing(
        profile: CaImportFixtures.Profile,
        keys: java.security.KeyPair = CaImportFixtures.p256(),
    ) {
        val certificate = Pem.certificate(CaImportFixtures.certificate(keys, profile))
        CaImportFixtures.source(root, certificate, Pem.privateKey(keys.private))
    }

    private fun refusedWith(
        profile: CaImportFixtures.Profile,
        reason: CaImportRefusal,
    ): String {
        importing(profile)
        return refusal(reason = reason)
    }

    @Test
    fun `an intermediate CA signed by another CA is refused naming subject and issuer`() {
        val other = CaImportFixtures.p256()
        val profile = CaImportFixtures.Profile(subject = "CN=qa-inter", issuer = "CN=qa-ca", signer = other.private)
        val message = refusedWith(profile, CaImportRefusal.CA_NOT_SELF_SIGNED)
        assertTrue("CN=qa-inter" in message && "CN=qa-ca" in message, message)
    }

    @Test
    fun `a self-signed certificate that is not a CA is refused naming its subject`() {
        for (flag in listOf(false, null)) {
            val profile = CaImportFixtures.Profile(subject = "CN=qa-leaf", basicConstraints = flag)
            val message = refusedWith(profile, CaImportRefusal.CA_NOT_A_CA)
            assertTrue("CN=qa-leaf" in message, message)
        }
    }

    @Test
    fun `a key usage without keyCertSign is refused, no key usage extension is accepted`() {
        val profile = CaImportFixtures.Profile(keyUsage = KeyUsage.digitalSignature)
        val message = refusedWith(profile, CaImportRefusal.CA_KEY_USAGE)
        assertTrue("keyCertSign" in message, message)
        importing(CaImportFixtures.Profile(keyUsage = null))
        read()
    }

    @Test
    fun `a CA that is not valid yet is refused naming notBefore and the server clock`() {
        val notBefore = Instant.parse("2026-10-08T13:00:00Z")
        val message = refusedWith(CaImportFixtures.Profile(notBefore = notBefore), CaImportRefusal.CA_NOT_YET_VALID)
        assertTrue("2026-10-08T13:00:00Z" in message && "2026-10-08T12:00:00Z" in message, message)
        importing(CaImportFixtures.Profile(notBefore = Instant.parse("2026-10-08T12:00:00Z")))
        read()
    }

    @Test
    fun `a CA whose notAfter is now or past is refused naming notAfter`() {
        for (notAfter in listOf("2026-10-08T12:00:00Z", "2025-01-01T00:00:00Z")) {
            val profile = CaImportFixtures.Profile(notAfter = Instant.parse(notAfter))
            val message = refusedWith(profile, CaImportRefusal.CA_EXPIRED)
            assertTrue(notAfter in message, message)
        }
        importing(CaImportFixtures.Profile(notAfter = Instant.parse("2026-10-08T12:00:01Z")))
        read()
    }

    @Test
    fun `the first failing check names the reason, a mismatch comes before an expiry`() {
        val expired =
            CaImportFixtures.certificate(
                CaImportFixtures.p256(),
                CaImportFixtures.Profile(notAfter = Instant.parse("2025-01-01T00:00:00Z")),
            )
        CaImportFixtures.source(root, Pem.certificate(expired), Pem.privateKey(CaImportFixtures.p256().private))
        val message = refusal(reason = CaImportRefusal.CA_KEY_MISMATCH)
        assertTrue("CA_EXPIRED" !in message, message)
    }

    @Test
    fun `a CA with 90 days or less left is accepted with a warning naming notAfter and days`(output: CapturedOutput) {
        importing(CaImportFixtures.Profile(notAfter = Instant.parse("2027-01-06T12:00:00Z")))
        read()
        val warned = output.all.lines().any { "WARN" in it && "CA expires 2027-01-06T12:00:00Z, 90 days left" in it }
        assertTrue(warned, output.all)
        importing(CaImportFixtures.Profile(notAfter = Instant.parse("2026-10-09T12:00:00Z")))
        read()
        assertTrue("CA expires 2026-10-09T12:00:00Z, 1 day left" in output.all, output.all)
    }

    @Test
    fun `a CA with more than 90 days left is accepted without a warning`(output: CapturedOutput) {
        importing(CaImportFixtures.Profile(notAfter = Instant.parse("2027-01-06T12:00:01Z")))
        read()
        read(root)
        assertFalse("CA expires" in output.all, output.all)
    }

    private fun reconcile(present: CaFingerprint) = MutFlow.underTest { CaImportSource(root, CLOCK).reconcile(present) }

    @Test
    fun `the same CA already present needs no import and says so at INFO`(output: CapturedOutput) {
        CaImportFixtures.source(root, original)
        val fingerprint = CaFingerprint.of(original.certificate)
        reconcile(fingerprint)
        val line = output.all.lines().single { "CA import not needed" in it }
        assertTrue("INFO" in line && fingerprint.hex in line, line)
    }

    @Test
    fun `another CA already present is refused naming both fingerprints`() {
        CaImportFixtures.source(root, original)
        val present = CaFingerprint.of(CaImportFixtures.original().certificate)
        val e = assertFailsWith<CaImportRefused> { reconcile(present) }
        assertEquals(CaImportRefusal.CA_ALREADY_PRESENT, e.reason)
        val message = e.message.orEmpty()
        assertTrue(present.hex in message && CaFingerprint.of(original.certificate).hex in message, message)
    }

    @Test
    fun `a source that cannot be read while a CA is present only warns`(output: CapturedOutput) {
        reconcile(CaFingerprint.of(original.certificate))
        val line = output.all.lines().single { "CA import skipped" in it }
        assertTrue("WARN" in line && "SARD_PKI_IMPORT_DIR" in line && root.toString() in line, line)
        assertTrue("remove SARD_PKI_IMPORT_DIR" in line, line)
    }

    @Test
    fun `a CA with a path length limit of zero is still a CA`() {
        importing(CaImportFixtures.Profile(pathLength = 0))
        read()
    }
}
