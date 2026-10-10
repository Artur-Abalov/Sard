// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.CaImportFixtures.CLOCK
import dev.sard.server.pki.CaImportFixtures.NOW
import io.github.anschnapp.mutflow.MutFlow
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.FileTime
import java.nio.file.attribute.PosixFilePermission
import java.nio.file.attribute.PosixFilePermissions
import java.security.KeyPair
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/** The CA directory against the in-memory ledger: shared fixtures of the tests of [CaDirectory.open]. */
abstract class CaDirectoryLedgerBase {
    @TempDir
    lateinit var tmp: Path

    protected val dir get() = tmp.resolve("pki")
    protected val importDir get() = tmp.resolve("import")
    protected val ledger = FakeCaLedger()
    protected val g = CaImportFixtures.original()
    protected val f = CaImportFixtures.original()
    protected val gHex get() = CaFingerprint.of(g.certificate)
    protected val fHex get() = CaFingerprint.of(f.certificate)

    protected fun open(
        withSource: Boolean = false,
        writer: (Path, String) -> Unit = ::writeOwnerOnly,
        generated: CaKeyPair = g,
        listener: CaReplacementListener = CaReplacementListener { _, _ -> },
        isReadable: (Path) -> Boolean = { true },
    ): OpenedCa =
        MutFlow.underTest {
            CaDirectory(dir, CLOCK, ledger, writer, listener)
                .open(if (withSource) CaImportSource(importDir, CLOCK, isReadable) else null) { generated }
        }

    protected fun writeOwnerOnly(
        path: Path,
        text: String,
    ) {
        Files.writeString(path, text)
        Files.setPosixFilePermissions(path, PosixFilePermissions.fromString("rw-------"))
    }

    protected fun entries(path: Path) = Files.list(path).use { it.map { e -> e.fileName.toString() }.sorted().toList() }

    protected fun contentOf(path: Path) = if (Files.isRegularFile(path)) Files.readString(path) else ""

    protected fun snapshot(root: Path): List<String> =
        Files.walk(root).use { stream ->
            stream
                .filter { it != root }
                .map { "$it ${Files.getLastModifiedTime(it)} ${contentOf(it)}" }
                .sorted()
                .toList()
        }

    protected fun keyOf(root: Path) = Files.readString(root.resolve("ca/ca.key"))

    /** G is in the CA directory with its origin recorded, as after a first start. */
    protected fun firstStart() {
        open()
        assertEquals(CaProvenance.GENERATED, ledger.recorded[gHex])
    }

    // ---- Shared by every test class over the CA directory: they all reach the store and its leftovers ----

    @Test
    fun `Замена, прерванная между переименованиями, возвращает прежний CA при следующем старте`() {
        firstStart()
        Files.move(dir.resolve("ca"), dir.resolve(".tmp-replaced-crashed"))
        Files.setLastModifiedTime(
            Files.createDirectory(dir.resolve(".tmp-fresh")),
            FileTime.from(NOW),
        )

        val restarted = open(generated = f)

        assertEquals(gHex, CaFingerprint.of(restarted.pair.certificate))
        assertEquals(Pem.privateKey(g.privateKey), keyOf(dir))
        assertEquals(listOf(".tmp-fresh", "ca"), entries(dir))
    }

    @Test
    fun `Каталог подготовки старше часа стирается, моложе остаётся, чужие записи не трогаются`() {
        firstStart()
        val ages =
            mapOf(
                ".tmp-old" to Duration.ofHours(2),
                ".tmp-hour" to Duration.ofHours(1),
                ".tmp-young" to Duration.ofMinutes(5),
                "unrelated" to Duration.ofHours(5),
            )
        for ((name, age) in ages) {
            val path = Files.createDirectory(dir.resolve(name))
            Files.setLastModifiedTime(path, FileTime.from(NOW - age))
        }

        open()

        assertEquals(listOf(".tmp-hour", ".tmp-young", "ca", "unrelated"), entries(dir))
    }

    @Test
    fun `Ключ, сертификат, каталог CA или каталог ключей, открытые другим, — отказ запуска`() {
        firstStart()
        for (path in listOf("ca/ca.key", "ca/ca.crt", "ca", ".")) {
            val target = dir.resolve(path)
            val before = Files.getPosixFilePermissions(target)
            Files.setPosixFilePermissions(target, before + PosixFilePermission.OTHERS_READ)

            assertFailsWith<InsecureKeyStorageException>(path) { open() }

            Files.setPosixFilePermissions(target, before)
        }
        assertEquals(gHex, CaFingerprint.of(open().pair.certificate))
    }

    @Test
    fun `Сертификат в каталоге CA, подписанный чужим ключом, — отказ запуска`() {
        firstStart()
        val keys = KeyPair(g.certificate.publicKey, g.privateKey)
        val forged = PkiFixtures.rootLike(keys, CaImportFixtures.p256().private, ca = true)
        Files.writeString(dir.resolve("ca/ca.crt"), Pem.certificate(forged))

        val refused = assertFailsWith<IllegalStateException> { open() }

        assertEquals("CA certificate CN=Sard CA is not self-signed", refused.message)
    }
}
