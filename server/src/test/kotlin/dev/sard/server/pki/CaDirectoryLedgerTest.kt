// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.CaImportFixtures.CLOCK
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.dao.DataAccessResourceFailureException
import java.nio.file.Files
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
 * Docs/specs/server/onboarding-setup.feature, rules "Каталог CA и база ставятся вместе…" (Р19) and the leftovers
 * of start-ups that died: the decisions of the CA directory against the ledger of the database, without a source.
 */
@MutFlowTest
class CaDirectoryLedgerTest : CaDirectoryLedgerBase() {
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
    fun `Отказ без записанного происхождения не раскрывает материал ключа`() {
        firstStart()
        ledger.recorded.clear()

        val refused = assertFailsWith<CaStartRefused> { open() }

        val seen = generateSequence<Throwable>(refused) { it.cause }.mapNotNull { it.message }.joinToString("\n")
        for (fragment in CaImportFixtures.keyFragments(Pem.privateKey(g.privateKey))) {
            assertFalse(fragment in seen, "key line leaked")
        }
    }

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
    fun `Пустой каталог при базе, где CA не в деле, получает новый CA`() {
        ledger.recorded[gHex] = CaProvenance.GENERATED

        val opened = open(generated = f)

        assertEquals(fHex, CaFingerprint.of(opened.pair.certificate))
        assertEquals(CaProvenance.GENERATED, ledger.recorded[fHex])
        assertNotEquals(gHex, fHex)
    }
}
