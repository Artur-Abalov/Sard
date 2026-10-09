// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.CaImportFixtures.CLOCK
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.io.TempDir
import org.springframework.dao.DataAccessResourceFailureException
import java.io.IOException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.PosixFilePermissions
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

/**
 * Docs/specs/server/onboarding-setup.feature, rules "Сгенерированный CA заменяется импортом…" (Р11),
 * "До шага ca нечитаемый или неподходящий источник…" (Р18) and "Каталог CA и база ставятся вместе…" (Р19):
 * the decisions of the CA directory against the ledger of the database.
 */
@MutFlowTest
class CaDirectoryLedgerTest {
    @TempDir
    lateinit var tmp: Path

    private val dir get() = tmp.resolve("pki")
    private val importDir get() = tmp.resolve("import")
    private val ledger = FakeCaLedger()
    private val g = CaImportFixtures.original()
    private val f = CaImportFixtures.original()
    private val gHex get() = CaFingerprint.of(g.certificate)
    private val fHex get() = CaFingerprint.of(f.certificate)

    private fun open(
        withSource: Boolean = false,
        writer: (Path, String) -> Unit = ::writeOwnerOnly,
        generated: CaKeyPair = g,
    ): OpenedCa =
        MutFlow.underTest {
            CaDirectory(dir, CLOCK, ledger, writer)
                .open(if (withSource) CaImportSource(importDir, CLOCK) else null) { generated }
        }

    private fun writeOwnerOnly(
        path: Path,
        text: String,
    ) {
        Files.writeString(path, text)
        Files.setPosixFilePermissions(path, PosixFilePermissions.fromString("rw-------"))
    }

    private fun entries(path: Path) = Files.list(path).use { it.map { e -> e.fileName.toString() }.sorted().toList() }

    private fun contentOf(path: Path) = if (Files.isRegularFile(path)) Files.readString(path) else ""

    private fun snapshot(root: Path): List<String> =
        Files.walk(root).use { stream ->
            stream
                .filter { it != root }
                .map { "$it ${Files.getLastModifiedTime(it)} ${contentOf(it)}" }
                .sorted()
                .toList()
        }

    private fun keyOf(root: Path) = Files.readString(root.resolve("ca/ca.key"))

    /** G is in the CA directory with its origin recorded, as after a first start. */
    private fun firstStart() {
        open()
        assertEquals(CaProvenance.GENERATED, ledger.recorded[gHex])
    }

    // ---- Происхождение записывается раньше, чем CA появляется ----

    @Test
    fun `Первый старт записывает происхождение generated раньше, чем CA появляется в каталоге`() {
        var seen: Boolean? = null
        ledger.onRecord = { _, _ -> seen = Files.exists(dir.resolve("ca")) }

        val opened = open()

        assertEquals(false, seen)
        assertEquals(CaOrigin.GENERATED, opened.origin)
        assertEquals(CaProvenance.GENERATED, opened.provenance)
        assertEquals(CaProvenance.GENERATED, ledger.recorded[gHex])
    }

    @Test
    fun `Импорт в пустой каталог записывает imported раньше, чем CA появляется`() {
        CaImportFixtures.source(importDir, f)
        var seen: Boolean? = null
        ledger.onRecord = { _, _ -> seen = Files.exists(dir.resolve("ca")) }

        val opened = open(withSource = true)

        assertEquals(false, seen)
        assertEquals(CaProvenance.IMPORTED, opened.provenance)
        assertEquals(CaProvenance.IMPORTED, ledger.recorded[fHex])
    }

    @Test
    fun `Импорт в пустой каталог заменяет запись с тем же отпечатком`() {
        ledger.recorded[fHex] = CaProvenance.GENERATED
        ledger.usage = CaUsage.STEP_CA_COMPLETE
        CaImportFixtures.source(importDir, f)

        open(withSource = true)

        assertEquals(CaProvenance.IMPORTED, ledger.recorded[fHex])
    }

    @Test
    fun `Сбой записи происхождения оставляет каталог CA пустым без временных каталогов`() {
        ledger.failRecording = true

        assertFailsWith<DataAccessResourceFailureException> { open() }

        assertTrue(Files.notExists(dir.resolve("ca")))
        assertEquals(emptyList(), entries(dir))
    }

    @Test
    fun `Недоступная база при первом старте не создаёт CA`() {
        ledger.down = true

        assertFailsWith<DataAccessResourceFailureException> { open() }

        assertTrue(Files.notExists(dir.resolve("ca")))
    }

    @Test
    fun `Перезапуск читает происхождение из базы, а в журнале старта остаётся existing`() {
        firstStart()

        val again = open()

        assertEquals(CaOrigin.EXISTING, again.origin)
        assertEquals(CaProvenance.GENERATED, again.provenance)
    }

    @Test
    fun `Одновременные первые старты работают с одним CA, происхождение которого записано`() {
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(2)
        try {
            val results =
                listOf(g, f).map { generated ->
                    pool.submit<Pair<CaFingerprint, CaProvenance>> {
                        start.await()
                        val opened = CaDirectory(dir, CLOCK, ledger).open(null) { generated }
                        CaFingerprint.of(opened.pair.certificate) to opened.provenance
                    }
                }
            start.countDown()
            val outcomes = results.map { it.get() }
            assertEquals(1, outcomes.map { it.first }.toSet().size)
            assertEquals(setOf(CaProvenance.GENERATED), outcomes.map { it.second }.toSet())
            assertEquals(CaProvenance.GENERATED, ledger.recorded[outcomes.first().first])
        } finally {
            pool.shutdownNow()
        }
    }

    // ---- Р19 а: CA без записанного происхождения ----

    @Test
    fun `CA в каталоге без записанного происхождения — отказ CA_ORIGIN_NOT_RECORDED, ничего не меняется`() {
        firstStart()
        ledger.recorded.clear()
        val before = snapshot(dir)

        val refused = assertFailsWith<CaStartRefused> { open() }

        assertEquals(CaStartRefusal.CA_ORIGIN_NOT_RECORDED, refused.reason)
        val message = refused.message.orEmpty()
        assertTrue(message.startsWith("CA startup refused"), message)
        assertTrue(gHex.hex in message, message)
        assertTrue(dir.resolve("ca").toString() in message, message)
        assertTrue("CA origin is not recorded in the database" in message, message)
        assertTrue("server volumes are reinstalled together" in message, message)
        assertTrue("docs/operator/09-troubleshooting.md" in message, message)
        assertEquals(before, snapshot(dir))
        assertNull(ledger.recorded[gHex])
    }

    @Test
    fun `Запись о другом CA не заменяет запись об этом`() {
        firstStart()
        ledger.recorded.clear()
        ledger.recorded[fHex] = CaProvenance.GENERATED
        ledger.usage = CaUsage.STEP_CA_COMPLETE

        assertEquals(CaStartRefusal.CA_ORIGIN_NOT_RECORDED, assertFailsWith<CaStartRefused> { open() }.reason)
    }

    @Test
    fun `Отказ без записанного происхождения не зависит от источника импорта`() {
        firstStart()
        ledger.recorded.clear()
        val variants =
            listOf<(Path) -> Unit>(
                { },
                { CaImportFixtures.source(it, g) },
                { CaImportFixtures.source(it, f) },
            )
        for (prepare in variants) {
            prepare(importDir)
            val sourceBefore = if (Files.exists(importDir)) snapshot(importDir) else emptyList()
            val before = snapshot(dir)

            val refused = assertFailsWith<CaStartRefused> { open(withSource = true) }

            assertEquals(CaStartRefusal.CA_ORIGIN_NOT_RECORDED, refused.reason)
            assertEquals(before, snapshot(dir))
            assertEquals(sourceBefore, if (Files.exists(importDir)) snapshot(importDir) else emptyList())
            importDir.toFile().deleteRecursively()
        }
    }

    @Test
    fun `Отказ без записанного происхождения не раскрывает материал ключа`() {
        firstStart()
        ledger.recorded.clear()

        val refused = assertFailsWith<CaStartRefused> { open() }

        val seen = generateSequence<Throwable>(refused) { it.cause }.mapNotNull { it.message }.joinToString("\n")
        for (fragment in CaImportFixtures.keyFragments(Pem.privateKey(g.privateKey))) {
            assertFalse(fragment in seen, "key line leaked")
        }
    }

    // ---- Р19 б: пустой каталог при CA в деле ----

    @Test
    fun `Пустой каталог без источника при CA в деле — отказ CA_MISSING с причиной`() {
        for (
        (usage, reason) in
        listOf(
            CaUsage.STEP_CA_COMPLETE to "onboarding step ca is complete",
            CaUsage.AGENT_CERTIFICATES to "agent certificates issued",
        )
        ) {
            ledger.usage = usage

            val refused = assertFailsWith<CaStartRefused> { open() }

            assertEquals(CaStartRefusal.CA_MISSING, refused.reason)
            val message = refused.message.orEmpty()
            assertTrue(message.startsWith("CA startup refused"), message)
            assertTrue(dir.resolve("ca").toString() in message, message)
            assertTrue("CA directory is empty" in message, message)
            assertTrue(reason in message, message)
            assertTrue("SARD_PKI_IMPORT_DIR" in message, message)
            assertTrue("server volumes are reinstalled together" in message, message)
            assertTrue("docs/operator/09-troubleshooting.md" in message, message)
            assertEquals(emptyList(), entries(dir))
        }
    }

    @Test
    fun `Пустой каталог с источником при восстановленной базе импортирует CA как раньше`() {
        ledger.usage = CaUsage.AGENT_CERTIFICATES
        ledger.recorded[fHex] = CaProvenance.GENERATED
        CaImportFixtures.source(importDir, f)

        val opened = open(withSource = true)

        assertEquals(fHex, CaFingerprint.of(opened.pair.certificate))
        assertEquals(CaProvenance.IMPORTED, ledger.recorded[fHex])
    }

    @Test
    fun `Пустой каталог при базе, где CA не в деле, получает новый CA`() {
        ledger.recorded[gHex] = CaProvenance.GENERATED

        val opened = open(generated = f)

        assertEquals(fHex, CaFingerprint.of(opened.pair.certificate))
        assertEquals(CaProvenance.GENERATED, ledger.recorded[fHex])
        assertNotEquals(gHex, fHex)
    }

    // ---- Р11: замена ----

    private fun replacing(): OpenedCa {
        firstStart()
        CaImportFixtures.source(importDir, f)
        return open(withSource = true)
    }

    @Test
    fun `Пока шаг ca не выполнен и сертификатов нет, источник с другим CA заменяет сгенерированный`() {
        val opened = replacing()

        assertEquals(fHex, CaFingerprint.of(opened.pair.certificate))
        assertEquals(gHex, opened.replaced)
        assertEquals(CaOrigin.IMPORTED, opened.origin)
        assertEquals(CaProvenance.IMPORTED, opened.provenance)
        assertEquals(CaProvenance.IMPORTED, ledger.recorded[fHex])
    }

    @Test
    fun `После замены в каталоге CA ровно ca с ca crt и ca key, от прежнего CA ничего не осталось`() {
        firstStart()
        val oldKey = keyOf(dir)
        CaImportFixtures.source(importDir, f)
        open(withSource = true)

        assertEquals(listOf("ca"), entries(dir))
        assertEquals(listOf("ca.crt", "ca.key"), entries(dir.resolve("ca")))

        fun modeOf(path: String) = PosixFilePermissions.toString(Files.getPosixFilePermissions(dir.resolve(path)))
        assertEquals("rwx------", modeOf("ca"))
        assertEquals("rw-------", modeOf("ca/ca.key"))
        assertEquals(Pem.privateKey(f.privateKey), keyOf(dir))
        for (line in CaImportFixtures.keyFragments(oldKey)) {
            val anywhere = Files.walk(dir).use { s -> s.anyMatch { line in contentOf(it) } }
            assertFalse(anywhere, "a line of the old key is left")
        }
    }

    @Test
    fun `Происхождение нового CA записано раньше замены`() {
        firstStart()
        CaImportFixtures.source(importDir, f)
        var oldCaStillThere: Boolean? = null
        ledger.onRecord = { _, _ -> oldCaStillThere = keyOf(dir) == Pem.privateKey(g.privateKey) }

        open(withSource = true)

        assertEquals(true, oldCaStillThere)
    }

    @Test
    fun `Источник с тем же CA до шага ca не меняет каталог`() {
        firstStart()
        CaImportFixtures.source(importDir, g)
        val before = snapshot(dir)

        val opened = open(withSource = true)

        assertEquals(CaOrigin.EXISTING, opened.origin)
        assertEquals(null, opened.replaced)
        assertEquals(before, snapshot(dir))
    }

    @Test
    fun `Импортированный, но не подтверждённый CA заменяется другим импортом`() {
        CaImportFixtures.source(importDir, f)
        open(withSource = true)
        val h = CaImportFixtures.original()
        importDir.toFile().deleteRecursively()
        CaImportFixtures.source(importDir, h)

        val opened = open(withSource = true)

        assertEquals(CaFingerprint.of(h.certificate), CaFingerprint.of(opened.pair.certificate))
        assertEquals(fHex, opened.replaced)
    }

    @Test
    fun `Выданный сертификат агента запрещает замену и каталог не меняется`() {
        firstStart()
        ledger.usage = CaUsage.AGENT_CERTIFICATES
        CaImportFixtures.source(importDir, f)
        val before = snapshot(dir)

        val refused = assertFailsWith<CaImportRefused> { open(withSource = true) }

        assertEquals(CaImportRefusal.CA_ALREADY_PRESENT, refused.reason)
        assertTrue("agent certificates issued" in refused.message.orEmpty(), refused.message)
        assertEquals(before, snapshot(dir))
    }

    @Test
    fun `Выполненный шаг ca запрещает замену и каталог не меняется`() {
        firstStart()
        ledger.usage = CaUsage.STEP_CA_COMPLETE
        CaImportFixtures.source(importDir, f)
        val before = snapshot(dir)

        val refused = assertFailsWith<CaImportRefused> { open(withSource = true) }

        assertTrue("onboarding step ca is complete" in refused.message.orEmpty(), refused.message)
        assertEquals(before, snapshot(dir))
    }

    @Test
    fun `Неподходящий источник при замене не трогает прежний CA`() {
        firstStart()
        val before = snapshot(dir)
        for (
        spoil in
        listOf<(Path) -> Unit>(
            { CaImportFixtures.chmod(it.resolve("ca/ca.key"), "rw-r-----") },
            { Files.delete(it.resolve("ca/ca.crt")) },
        )
        ) {
            importDir.toFile().deleteRecursively()
            CaImportFixtures.source(importDir, f)
            spoil(importDir)

            assertFailsWith<CaImportRefused> { open(withSource = true) }

            assertEquals(before, snapshot(dir))
        }
    }

    @Test
    fun `Сбой записи при замене оставляет прежний CA целиком`() {
        firstStart()
        CaImportFixtures.source(importDir, f)
        val before = snapshot(dir)
        val failing = { path: Path, _: String ->
            if (path.fileName.toString() == "ca.key") throw IOException("No space left on device")
        }

        val refused = assertFailsWith<CaImportRefused> { open(withSource = true, writer = failing) }

        assertEquals(CaImportRefusal.IMPORT_WRITE_FAILED, refused.reason)
        assertEquals(before, snapshot(dir))
    }

    @Test
    fun `Прерванная замена оставляет либо прежний, либо новый CA целиком`() {
        // Each write is cut off in turn: the key, then the certificate of the staged CA.
        for (cut in listOf("ca.key", "ca.crt")) {
            importDir.toFile().deleteRecursively()
            dir.toFile().deleteRecursively()
            ledger.recorded.clear()
            firstStart()
            CaImportFixtures.source(importDir, f)
            val failing = { path: Path, text: String ->
                if (path.fileName.toString() == cut) throw IOException("cut") else writeOwnerOnly(path, text)
            }
            assertFailsWith<CaImportRefused> { open(withSource = true, writer = failing) }

            val restarted = open()

            assertEquals(gHex, CaFingerprint.of(restarted.pair.certificate), cut)
            assertEquals(Pem.privateKey(g.privateKey), keyOf(dir), cut)
        }
    }

    @Test
    fun `Замена, прерванная между переименованиями, возвращает прежний CA при следующем старте`() {
        firstStart()
        Files.move(dir.resolve("ca"), dir.resolve(".tmp-replaced-crashed"))

        val restarted = open()

        assertEquals(gHex, CaFingerprint.of(restarted.pair.certificate))
        assertEquals(listOf("ca"), entries(dir))
    }

    @Test
    fun `Замена, прерванная после переименований, стирает остатки прежнего CA`() {
        firstStart()
        val oldKey = keyOf(dir)
        CaImportFixtures.source(importDir, f)
        open(withSource = true)
        val leftover = Files.createDirectory(dir.resolve(".tmp-replaced-crashed"))
        Files.writeString(leftover.resolve("ca.key"), oldKey)

        val restarted = open()

        assertEquals(fHex, CaFingerprint.of(restarted.pair.certificate))
        assertEquals(listOf("ca"), entries(dir))
    }

    @Test
    fun `Недоступная база при старте с источником не меняет каталог CA`() {
        firstStart()
        CaImportFixtures.source(importDir, f)
        val before = snapshot(dir)
        ledger.down = true

        assertFailsWith<DataAccessResourceFailureException> { open(withSource = true) }

        assertEquals(before, snapshot(dir))
    }

    @Test
    fun `Замена не раскрывает материал ключей в сообщениях`() {
        firstStart()
        CaImportFixtures.source(importDir, f)
        val failing = { _: Path, _: String -> throw IOException("No space left on device") }

        val refused = assertFailsWith<CaImportRefused> { open(withSource = true, writer = failing) }

        for (pem in listOf(g, f).map { Pem.privateKey(it.privateKey) }) {
            for (fragment in CaImportFixtures.keyFragments(pem)) assertFalse(fragment in refused.message.orEmpty())
        }
    }
}
