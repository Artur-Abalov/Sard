// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import ch.qos.logback.classic.Level
import dev.sard.server.pki.CaImportFixtures.CLOCK
import dev.sard.server.pki.CaImportFixtures.NOW
import dev.sard.server.selfagent.captureEvents
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.bouncycastle.asn1.x509.KeyUsage
import org.springframework.dao.DataAccessResourceFailureException
import java.io.IOException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.PosixFilePermissions
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFails
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * Docs/specs/server/onboarding-setup.feature, rules "Сгенерированный CA заменяется импортом…" (Р11) and
 * "До шага ca нечитаемый или неподходящий источник…" (Р18): the CA directory against the real import source.
 */
@MutFlowTest
class CaDirectorySourceTest : CaDirectoryLedgerBase() {
    private fun replacing(): OpenedCa {
        firstStart()
        CaImportFixtures.source(importDir, f)
        return open(withSource = true)
    }

    private fun freshSource(prepare: (Path) -> Unit = {}) {
        importDir.toFile().deleteRecursively()
        CaImportFixtures.source(importDir, f)
        prepare(importDir)
    }

    private fun importing(profile: CaImportFixtures.Profile) {
        importDir.toFile().deleteRecursively()
        val keys = CaImportFixtures.p256()
        val certificate = Pem.certificate(CaImportFixtures.certificate(keys, profile))
        CaImportFixtures.source(importDir, certificate, Pem.privateKey(keys.private))
    }

    private fun assertRefused(
        reason: CaImportRefusal,
        isReadable: (Path) -> Boolean = { true },
    ) {
        val before = snapshot(dir)

        val refused = assertFailsWith<CaImportRefused> { open(withSource = true, isReadable = isReadable) }

        assertEquals(reason, refused.reason)
        assertEquals(before, snapshot(dir))
        assertEquals(Pem.privateKey(g.privateKey), keyOf(dir))
    }

    @Test
    fun `Импорт в пустой каталог записывает imported раньше, чем CA появляется`() {
        CaImportFixtures.source(importDir, f)
        var seen: Boolean? = null
        ledger.onRecord = { _, _ -> seen = Files.exists(dir.resolve("ca")) }

        val opened = open(withSource = true)

        assertEquals(false, seen)
        assertEquals(CaOrigin.IMPORTED, opened.origin)
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
    fun `Пустой каталог с источником при восстановленной базе импортирует CA как раньше`() {
        ledger.usage = CaUsage.AGENT_CERTIFICATES
        ledger.recorded[fHex] = CaProvenance.GENERATED
        CaImportFixtures.source(importDir, f)

        val opened = open(withSource = true)

        assertEquals(fHex, CaFingerprint.of(opened.pair.certificate))
        assertEquals(CaProvenance.IMPORTED, ledger.recorded[fHex])
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
    fun `Прерванное создание CA не оставляет CA без записанного происхождения`() {
        // The first start of a clean installation and the first start with a source, each cut off in turn.
        for (imported in listOf(false, true)) {
            for (cut in listOf("ca.key", "ca.crt")) {
                dir.toFile().deleteRecursively()
                importDir.toFile().deleteRecursively()
                ledger.recorded.clear()
                if (imported) CaImportFixtures.source(importDir, f)
                val failing = { path: Path, text: String ->
                    if (path.fileName.toString() == cut) throw IOException("cut") else writeOwnerOnly(path, text)
                }
                assertFails { open(withSource = imported, writer = failing) }

                val restarted = open()

                assertEquals(
                    CaProvenance.GENERATED,
                    ledger.recorded[CaFingerprint.of(restarted.pair.certificate)],
                    "imported=$imported, cut at $cut",
                )
            }
        }
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
    fun `a replacement whose listener fails leaves the CA directory as it was, the next start replaces and tells`() {
        firstStart()
        CaImportFixtures.source(importDir, f)
        val before = snapshot(dir)

        assertFailsWith<IllegalStateException> {
            open(withSource = true, listener = { _, _ -> error("revocation failed") })
        }

        assertEquals(before, snapshot(dir))
        val told = mutableListOf<Pair<CaFingerprint, CaFingerprint>>()

        val opened = open(withSource = true, listener = { previous, current -> told += previous to current })

        assertEquals(fHex, CaFingerprint.of(opened.pair.certificate))
        assertEquals(listOf(gHex to fHex), told)
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

    @Test
    fun `Источник, которого нет или в котором нет файла, останавливает старт до замены`() {
        firstStart()
        importDir.toFile().deleteRecursively()
        assertRefused(CaImportRefusal.IMPORT_SOURCE_MISSING)
        for (file in listOf("ca.crt", "ca.key")) {
            freshSource { Files.delete(it.resolve("ca/$file")) }
            assertRefused(CaImportRefusal.IMPORT_FILE_MISSING)
        }
    }

    @Test
    fun `Источник, который сервер не может прочитать, останавливает старт до замены`() {
        firstStart()
        freshSource()
        for (name in listOf("", "ca", "ca/ca.crt", "ca/ca.key")) {
            val unreadable = importDir.resolve(name).normalize()
            assertRefused(CaImportRefusal.IMPORT_FILE_UNREADABLE) { it != unreadable }
        }
    }

    @Test
    fun `Источник с правами шире владельца останавливает старт до замены`() {
        firstStart()
        val cases =
            listOf(
                "ca/ca.key" to "rw-r-----",
                "ca/ca.crt" to "rw-r--r--",
                "ca" to "rwxr-x---",
                "." to "rwxr-xr-x",
            )
        for ((relative, mode) in cases) {
            freshSource { CaImportFixtures.chmod(it.resolve(relative).normalize(), mode) }
            assertRefused(CaImportRefusal.IMPORT_PERMISSIONS_TOO_OPEN)
        }
    }

    @Test
    fun `Источник с неподходящим содержимым файлов останавливает старт до замены`() {
        firstStart()
        val key = Pem.privateKey(f.privateKey)
        val certificate = Pem.certificate(f.certificate)
        val rsa = CaImportFixtures.rsa()
        val cases =
            listOf(
                Triple("not a certificate", key, CaImportRefusal.CA_CERT_INVALID),
                Triple(certificate, "not a key", CaImportRefusal.CA_KEY_INVALID),
                Triple(
                    Pem.certificate(CaImportFixtures.certificate(rsa)),
                    Pem.privateKey(rsa.private),
                    CaImportRefusal.CA_KEY_UNSUPPORTED,
                ),
                Triple(
                    certificate,
                    Pem.privateKey(CaImportFixtures.p256().private),
                    CaImportRefusal.CA_KEY_MISMATCH,
                ),
            )
        for ((certificateText, keyText, reason) in cases) {
            importDir.toFile().deleteRecursively()
            CaImportFixtures.source(importDir, certificateText, keyText)
            assertRefused(reason)
        }
    }

    @Test
    fun `Источник с CA, который не годится в корневой, останавливает старт до замены`() {
        firstStart()
        val other = CaImportFixtures.p256().private
        val cases =
            listOf(
                CaImportFixtures.Profile(issuer = "CN=other", signer = other) to CaImportRefusal.CA_NOT_SELF_SIGNED,
                CaImportFixtures.Profile(basicConstraints = false) to CaImportRefusal.CA_NOT_A_CA,
                CaImportFixtures.Profile(basicConstraints = null) to CaImportRefusal.CA_NOT_A_CA,
                CaImportFixtures.Profile(keyUsage = KeyUsage.digitalSignature) to CaImportRefusal.CA_KEY_USAGE,
                CaImportFixtures.Profile(notBefore = NOW.plusSeconds(1)) to CaImportRefusal.CA_NOT_YET_VALID,
                CaImportFixtures.Profile(notBefore = NOW.plusSeconds(3600)) to CaImportRefusal.CA_NOT_YET_VALID,
                CaImportFixtures.Profile(notAfter = NOW) to CaImportRefusal.CA_EXPIRED,
                CaImportFixtures.Profile(notAfter = NOW.minusSeconds(1)) to CaImportRefusal.CA_EXPIRED,
            )
        for ((profile, reason) in cases) {
            importing(profile)
            assertRefused(reason)
        }
    }

    @Test
    fun `Граничные, но годные CA заменяют прежний`() {
        val profiles =
            listOf(
                CaImportFixtures.Profile(pathLength = 0),
                CaImportFixtures.Profile(keyUsage = null),
                CaImportFixtures.Profile(notBefore = NOW),
                CaImportFixtures.Profile(notAfter = NOW.plusSeconds(1)),
            )
        for (profile in profiles) {
            dir.toFile().deleteRecursively()
            ledger.recorded.clear()
            firstStart()
            importing(profile)

            assertEquals(gHex, open(withSource = true).replaced, "$profile")
        }
    }

    @Test
    fun `CA, до которого осталось не больше 90 дней, принимается с предупреждением, дальше — без`() {
        val cases = mapOf(Duration.ofDays(90) to true, Duration.ofDays(90).plusSeconds(1) to false)
        for ((left, warned) in cases) {
            dir.toFile().deleteRecursively()
            ledger.recorded.clear()
            firstStart()
            importing(CaImportFixtures.Profile(notAfter = NOW + left))

            val events = captureEvents { open(withSource = true) }

            val warnings = events.filter { it.level == Level.WARN && "CA expires" in it.text }
            assertEquals(if (warned) 1 else 0, warnings.size, "$left: $events")
            if (warned) assertTrue("90 days left" in warnings.single().text, warnings.single().text)
        }
    }

    @Test
    fun `Источник с тем же CA сообщает, что импорт не нужен`() {
        firstStart()
        CaImportFixtures.source(importDir, g)

        val events = captureEvents { open(withSource = true) }

        val line = events.single { "CA import not needed" in it.text }
        assertEquals(Level.INFO, line.level)
        assertTrue(gHex.hex in line.text, line.text)
    }

    @Test
    fun `Источник импорта в пустой каталог тоже предупреждает о скором конце срока CA`() {
        importing(CaImportFixtures.Profile(notAfter = NOW + Duration.ofDays(90)))

        val events = captureEvents { open(withSource = true) }

        assertEquals(1, events.count { it.level == Level.WARN && "CA expires" in it.text }, events.toString())
    }

    @Test
    fun `Старт с источником, проигравший гонку, работает с CA победителя и называет его existing`() {
        CaImportFixtures.source(importDir, f)
        val real = CaImportSource(importDir, CLOCK)
        val racing =
            object : CaImport by real {
                override fun read(): ImportedCa {
                    CaDirectory(dir, CLOCK, ledger).loadOrCreate { g }
                    return real.read()
                }
            }

        val opened = MutFlow.underTest { CaDirectory(dir, CLOCK, ledger).open(racing) { error("not generated") } }

        assertEquals(CaOrigin.EXISTING, opened.origin)
        assertEquals(gHex, CaFingerprint.of(opened.pair.certificate))
    }
}
