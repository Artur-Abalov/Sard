// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.PkiFixtures.CLOCK
import dev.sard.server.pki.PkiFixtures.NOW
import dev.sard.server.pki.PkiFixtures.random
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.FileTime
import java.nio.file.attribute.PosixFilePermission
import java.nio.file.attribute.PosixFilePermissions
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertTrue

@MutFlowTest
class CaDirectoryTest {
    @TempDir
    lateinit var tmp: Path

    private fun generate() = CaKeyPair.generate(CLOCK, random())

    private fun perms(path: Path) = PosixFilePermissions.toString(Files.getPosixFilePermissions(path))

    private val dir get() = tmp.resolve("pki")

    @Test
    fun `the first start publishes the CA owner-only and a restart loads it`() {
        val pair = generate()
        val first = MutFlow.underTest { CaDirectory(dir, CLOCK).loadOrCreate { pair } }
        val second = CaDirectory(dir, CLOCK).loadOrCreate { error("must load, not generate") }
        assertEquals(pair.certificate, first.certificate)
        assertEquals(pair.certificate, second.certificate)
        val perms = listOf(dir, dir.resolve("ca"), dir.resolve("ca/ca.key")).map { perms(it) }
        assertEquals(listOf("rwx------", "rwx------", "rw-------"), perms)
    }

    @Test
    fun `losing the first-start race loads the winner's CA and leaves no temporary files`() {
        val winner = generate()
        val loser = generate()
        val loaded =
            MutFlow.underTest {
                CaDirectory(dir, CLOCK).loadOrCreate {
                    // Another instance publishes its CA while this one is generating.
                    CaDirectory(dir, CLOCK).loadOrCreate { winner }
                    loser
                }
            }
        assertEquals(CaFingerprint.of(winner.certificate), CaFingerprint.of(loaded.certificate))
        assertEquals(listOf("ca"), Files.list(dir).map { it.fileName.toString() }.toList())
    }

    @Test
    fun `concurrent first starts agree on one CA`() {
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(4)
        try {
            val results =
                (1..4).map {
                    pool.submit<CaFingerprint> {
                        start.await()
                        CaFingerprint.of(CaDirectory(dir, CLOCK).loadOrCreate { generate() }.certificate)
                    }
                }
            start.countDown()
            assertEquals(1, results.map { it.get() }.toSet().size)
        } finally {
            pool.shutdownNow()
        }
    }

    @Test
    fun `a key, certificate, CA directory or key directory open to others is refused`() {
        val pair = generate()
        for (path in listOf("ca/ca.key", "ca/ca.crt", "ca", ".")) {
            CaDirectory(dir, CLOCK).loadOrCreate { pair }
            val target = dir.resolve(path)
            val before = Files.getPosixFilePermissions(target)
            Files.setPosixFilePermissions(target, before + PosixFilePermission.OTHERS_READ)
            assertFailsWith<InsecureKeyStorageException>(path) {
                MutFlow.underTest { CaDirectory(dir, CLOCK).loadOrCreate { pair } }
            }
            Files.setPosixFilePermissions(target, before)
        }
    }

    @Test
    fun `a key that does not match the certificate is refused`() {
        CaDirectory(dir, CLOCK).loadOrCreate { generate() }
        val other = tmp.resolve("other")
        CaDirectory(other, CLOCK).loadOrCreate { generate() }
        Files.write(dir.resolve("ca/ca.key"), Files.readAllBytes(other.resolve("ca/ca.key")))
        val directory = CaDirectory(dir, CLOCK)
        assertFailsWith<IllegalStateException> { MutFlow.underTest { directory.loadOrCreate { generate() } } }
    }

    @Test
    fun `a certificate swapped for one with the same key but another signer is refused`() {
        val keys = Keys.generate(random())
        CaDirectory(dir, CLOCK).loadOrCreate { CaKeyPair(Certificates.root(keys, NOW, random()), keys.private) }
        val forged = PkiFixtures.rootLike(keys, Keys.generate(random()).private, ca = true)
        Files.writeString(dir.resolve("ca/ca.crt"), Pem.certificate(forged))
        val directory = CaDirectory(dir, CLOCK)
        val e = assertFailsWith<IllegalStateException> { MutFlow.underTest { directory.loadOrCreate { generate() } } }
        assertEquals("CA certificate CN=Sard CA is not self-signed", e.message)
    }

    @Test
    fun `staging directories older than an hour are removed, younger ones may belong to a running start`() {
        CaDirectory(dir, CLOCK).loadOrCreate { generate() }
        val ages = mapOf(".tmp-stale" to 120L, ".tmp-hour" to 60L, ".tmp-fresh" to 0L)
        for ((name, minutes) in ages) {
            val staging = Files.createDirectory(dir.resolve(name))
            Files.writeString(staging.resolve("ca.key"), "partial")
            Files.setLastModifiedTime(staging, FileTime.from(NOW - Duration.ofMinutes(minutes)))
        }
        MutFlow.underTest { CaDirectory(dir, CLOCK).loadOrCreate { error("must load") } }
        val left =
            Files
                .list(dir)
                .map { it.fileName.toString() }
                .sorted()
                .toList()
        assertEquals(listOf(".tmp-fresh", ".tmp-hour", "ca"), left)
    }

    @Test
    fun `owner-only paths pass the permission check`() {
        val ownerOnly = PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rw-------"))
        val file = Files.createFile(tmp.resolve("k"), ownerOnly)
        assertNull(MutFlow.underTest { ownerOnlyViolation(file) })
    }

    @Test
    fun `any group or other permission is a violation`() {
        val file = Files.createFile(tmp.resolve("k"))
        for (mode in listOf("rw----r--", "rw---x---", "rw-r-----", "rw-----w-")) {
            Files.setPosixFilePermissions(file, PosixFilePermissions.fromString(mode))
            val violation = MutFlow.underTest { ownerOnlyViolation(file) }
            assertTrue(violation != null && mode in violation, "$mode: $violation")
        }
    }
}
