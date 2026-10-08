// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.CaImportFixtures.CLOCK
import dev.sard.server.pki.CaImportFixtures.NOW
import org.junit.jupiter.api.extension.ExtendWith
import org.junit.jupiter.api.io.TempDir
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import java.io.IOException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.FileTime
import java.nio.file.attribute.PosixFilePermissions
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

@ExtendWith(OutputCaptureExtension::class)
class CaDirectoryImportTest {
    @TempDir
    lateinit var tmp: Path

    private val dir get() = tmp.resolve("pki")
    private val importDir get() = tmp.resolve("import")
    private val original = CaImportFixtures.original()
    private val generated = CaImportFixtures.original()

    private fun source() = CaImportSource(importDir, CLOCK)

    private fun open(source: CaImportSource? = source()): OpenedCa = CaDirectory(dir, CLOCK).open(source) { generated }

    private fun perms(path: Path) = PosixFilePermissions.toString(Files.getPosixFilePermissions(path))

    /** Names, modes, modification times and bytes of everything under [root]. */
    private fun snapshot(root: Path): List<String> =
        Files.walk(root).use { entries ->
            entries
                .map {
                    "$it ${perms(
                        it,
                    )} ${Files.getLastModifiedTime(it)} ${if (Files.isRegularFile(it)) Files.readString(it).hashCode() else ""}"
                }.sorted()
                .toList()
        }

    private fun entries(path: Path) = Files.list(path).use { it.map { e -> e.fileName.toString() }.sorted().toList() }

    @Test
    fun `an empty CA directory takes the CA of the source, stored like a generated one`() {
        CaImportFixtures.source(importDir, original)
        val before = snapshot(importDir)
        val opened = open()
        assertEquals(CaOrigin.IMPORTED, opened.origin)
        assertContentEquals(original.certificate.encoded, opened.pair.certificate.encoded)
        assertContentEquals(
            original.certificate.encoded,
            Files.readAllBytes(dir.resolve("ca/ca.crt")).let {
                PkiFixtures.certificate(String(it)).encoded
            },
        )
        assertEquals(
            listOf("rwx------", "rwx------", "rw-------", "rw-------"),
            listOf(dir, dir.resolve("ca"), dir.resolve("ca/ca.key"), dir.resolve("ca/ca.crt")).map(::perms),
        )
        assertEquals(listOf("ca"), entries(dir))
        assertEquals(listOf("ca.crt", "ca.key"), entries(dir.resolve("ca")))
        assertEquals(Pem.privateKey(original.privateKey), Files.readString(dir.resolve("ca/ca.key")))
        assertEquals(before, snapshot(importDir))
    }

    @Test
    fun `a CA directory that does not exist is created owner-only`() {
        CaImportFixtures.source(importDir, original)
        assertEquals(CaOrigin.IMPORTED, open().origin)
        assertEquals("rwx------", perms(dir))
    }

    @Test
    fun `a staging directory left by a crashed start does not block the import and is removed`() {
        CaImportFixtures.source(importDir, original)
        Files.createDirectory(dir, PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rwx------")))
        val stale = Files.createDirectory(dir.resolve(".tmp-crashed"))
        Files.writeString(stale.resolve("ca.key"), "partial")
        Files.setLastModifiedTime(stale, FileTime.from(NOW - Duration.ofHours(2)))
        assertEquals(CaOrigin.IMPORTED, open().origin)
        assertEquals(listOf("ca"), entries(dir))
    }

    @Test
    fun `without a source the CA is generated and a later start finds it existing`() {
        assertEquals(CaOrigin.GENERATED, open(null).origin)
        assertEquals(CaOrigin.EXISTING, open(null).origin)
    }

    @Test
    fun `the same source on the next start changes nothing`(output: CapturedOutput) {
        CaImportFixtures.source(importDir, original)
        open()
        val before = snapshot(dir)
        val second = open()
        assertEquals(CaOrigin.EXISTING, second.origin)
        assertEquals(before, snapshot(dir))
        assertTrue("CA import not needed" in output.all, output.all)
    }

    @Test
    fun `a source with another CA than the present one stops the start and changes nothing`() {
        open(null)
        CaImportFixtures.source(importDir, original)
        val before = snapshot(dir)
        val sourceBefore = snapshot(importDir)
        val e = assertFailsWith<CaImportRefused> { open() }
        assertEquals(CaImportRefusal.CA_ALREADY_PRESENT, e.reason)
        assertEquals(before, snapshot(dir))
        assertEquals(sourceBefore, snapshot(importDir))
    }

    @Test
    fun `a source that disappeared leaves the present CA in use`() {
        val first = open(null)
        val second = open()
        assertEquals(CaOrigin.EXISTING, second.origin)
        assertEquals(CaFingerprint.of(first.pair.certificate), CaFingerprint.of(second.pair.certificate))
    }

    @Test
    fun `a write that fails leaves no partial import and names the CA directory`() {
        CaImportFixtures.source(importDir, original)
        val before = snapshot(importDir)
        val failing = { path: Path, _: String ->
            if (path.fileName.toString() == "ca.key") throw IOException("No space left on device")
        }
        val e =
            assertFailsWith<CaImportRefused> {
                CaDirectory(dir, CLOCK, failing).open(source()) { error("never") }
            }
        assertEquals(CaImportRefusal.IMPORT_WRITE_FAILED, e.reason)
        val message = e.message.orEmpty()
        assertTrue(dir.toString() in message && "No space left on device" in message, message)
        assertEquals(emptyList(), entries(dir))
        assertEquals(before, snapshot(importDir))
        assertEquals(CaOrigin.IMPORTED, open().origin)
    }

    @Test
    fun `a CA directory that cannot be created is a write failure of the import`() {
        CaImportFixtures.source(importDir, original)
        val blocker = Files.createFile(tmp.resolve("blocker"))
        val e =
            assertFailsWith<CaImportRefused> {
                CaDirectory(blocker.resolve("pki"), CLOCK).open(source()) { error("never") }
            }
        assertEquals(CaImportRefusal.IMPORT_WRITE_FAILED, e.reason)
        assertTrue(blocker.resolve("pki").toString() in e.message.orEmpty(), e.message)
    }

    @Test
    fun `an import refused for its source leaves nothing in the CA directory and succeeds once fixed`() {
        CaImportFixtures.source(importDir, original)
        CaImportFixtures.chmod(importDir.resolve("ca/ca.key"), "rw-r-----")
        val e = assertFailsWith<CaImportRefused> { open() }
        assertEquals(CaImportRefusal.IMPORT_PERMISSIONS_TOO_OPEN, e.reason)
        assertEquals(emptyList(), entries(dir))
        CaImportFixtures.chmod(importDir.resolve("ca/ca.key"), "rw-------")
        assertEquals(CaOrigin.IMPORTED, open().origin)
    }

    @Test
    fun `concurrent first starts with one source end with one CA and no temporary files`() {
        CaImportFixtures.source(importDir, original)
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(2)
        try {
            val results =
                (1..2).map {
                    pool.submit<CaFingerprint> {
                        start.await()
                        CaFingerprint.of(CaDirectory(dir, CLOCK).open(source()) { error("never") }.pair.certificate)
                    }
                }
            start.countDown()
            assertEquals(setOf(CaFingerprint.of(original.certificate)), results.map { it.get() }.toSet())
        } finally {
            pool.shutdownNow()
        }
        assertEquals(listOf("ca"), entries(dir))
    }
}
