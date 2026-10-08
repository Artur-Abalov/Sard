// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.CaImportFixtures.CLOCK
import dev.sard.server.pki.CaImportFixtures.NOW
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.io.TempDir
import java.io.IOException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.FileTime
import java.nio.file.attribute.PosixFilePermissions
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

/** The decisions of [CaDirectory.open] with a fake source: what it asks the source and what it does with the answer. */
@MutFlowTest
class CaDirectoryImportUnitTest {
    @TempDir
    lateinit var tmp: Path

    private val dir get() = tmp.resolve("pki")
    private val original = CaImportFixtures.original()
    private val generated = CaImportFixtures.original()

    private class FakeImport(
        private val ca: ImportedCa,
    ) : CaImport {
        val reconciled = mutableListOf<CaFingerprint>()
        var reads = 0

        override fun read(): ImportedCa {
            reads++
            return ca
        }

        override fun reconcile(present: CaFingerprint) {
            reconciled += present
        }

        override fun writeFailed(
            caDirectory: Path,
            cause: IOException,
        ) = CaImportRefused(CaImportRefusal.IMPORT_WRITE_FAILED, "fake: $caDirectory ${cause.message}", caDirectory)
    }

    private fun imported(pair: CaKeyPair = original) = FakeImport(ImportedCa(pair.certificate, pair.privateKey))

    private fun open(
        source: CaImport?,
        writer: (Path, String) -> Unit = ::writeFile,
    ): OpenedCa = MutFlow.underTest { CaDirectory(dir, CLOCK, writer).open(source) { generated } }

    private fun writeFile(
        path: Path,
        text: String,
    ) {
        Files.writeString(path, text)
        Files.setPosixFilePermissions(path, PosixFilePermissions.fromString("rw-------"))
    }

    private fun ownerOnlyDir() {
        Files.createDirectory(dir, PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rwx------")))
    }

    @Test
    fun `an empty directory takes the CA the source gives and reports it imported`() {
        val source = imported()
        val opened = open(source)
        assertEquals(CaOrigin.IMPORTED, opened.origin)
        assertContentEquals(original.certificate.encoded, opened.pair.certificate.encoded)
        assertEquals(1, source.reads)
        assertEquals(emptyList(), source.reconciled)
        assertEquals("rwx------", PosixFilePermissions.toString(Files.getPosixFilePermissions(dir)))
    }

    @Test
    fun `without a source the CA is generated, and a CA in place is existing`() {
        assertEquals(CaOrigin.GENERATED, open(null).origin)
        assertEquals(CaOrigin.EXISTING, open(null).origin)
    }

    @Test
    fun `a CA in place is shown to the source and stays`() {
        open(null)
        val source = imported()
        val opened = open(source)
        assertEquals(CaOrigin.EXISTING, opened.origin)
        assertEquals(listOf(CaFingerprint.of(generated.certificate)), source.reconciled)
        assertEquals(0, source.reads)
    }

    @Test
    fun `a staging directory older than an hour is removed before the import, a younger one stays`() {
        ownerOnlyDir()
        for ((name, age) in mapOf(
            ".tmp-old" to Duration.ofHours(2),
            ".tmp-hour" to Duration.ofHours(1),
            ".tmp-young" to Duration.ofMinutes(5),
        )) {
            val staging = Files.createDirectory(dir.resolve(name))
            Files.setLastModifiedTime(staging, FileTime.from(NOW - age))
        }
        open(imported())
        val left = Files.list(dir).use { entries -> entries.map { it.fileName.toString() }.sorted().toList() }
        assertEquals(listOf(".tmp-hour", ".tmp-young", "ca"), left)
    }

    @Test
    fun `a CA directory open to others is refused before anything is read from the source`() {
        ownerOnlyDir()
        Files.setPosixFilePermissions(dir, PosixFilePermissions.fromString("rwxr-x---"))
        val source = imported()
        assertFailsWith<InsecureKeyStorageException> { open(source) }
        assertEquals(0, source.reads)
    }

    @Test
    fun `a failed write is the refusal the source builds, and nothing is left`() {
        val failing = { _: Path, _: String -> throw IOException("No space left on device") }
        val e = assertFailsWith<CaImportRefused> { open(imported(), failing) }
        assertEquals(CaImportRefusal.IMPORT_WRITE_FAILED, e.reason)
        assertTrue("No space left on device" in e.message.orEmpty(), e.message)
        assertEquals(emptyList(), Files.list(dir).use { it.toList() })
    }

    @Test
    fun `a failed write without a source is passed on as it is`() {
        val failing = { _: Path, _: String -> throw IOException("No space left on device") }
        val e = assertFailsWith<IOException> { open(null, failing) }
        assertEquals("No space left on device", e.message)
    }

    @Test
    fun `a key that does not belong to the certificate is refused by the CA pair`() {
        val other = CaImportFixtures.original()
        val mismatched = FakeImport(ImportedCa(original.certificate, other.privateKey))
        val e = assertFailsWith<IllegalStateException> { open(mismatched) }
        assertEquals("CA key does not match the CA certificate CN=Sard CA", e.message)
    }

    @Test
    fun `a certificate signed by another key is refused by the CA pair`() {
        val keys = CaImportFixtures.p256()
        val otherSigner = CaImportFixtures.p256().private
        val forged = CaImportFixtures.certificate(keys, CaImportFixtures.Profile(signer = otherSigner))
        val e = assertFailsWith<IllegalStateException> { open(FakeImport(ImportedCa(forged, keys.private))) }
        assertEquals("CA certificate CN=Sard CA is not self-signed", e.message)
    }
}
