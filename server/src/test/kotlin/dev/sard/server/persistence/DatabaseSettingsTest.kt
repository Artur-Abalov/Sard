// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

/** A database as a new installation creates it (DB1, D25): PostgreSQL 18, builtin C.UTF-8, UTC. */
private val NEW_INSTALLATION =
    DatabaseSettings(
        versionNum = 180006,
        version = "18.6",
        dataChecksums = true,
        encoding = "UTF8",
        localeProvider = "b",
        collate = "C",
        ctype = "C.UTF-8",
        locale = "C.UTF-8",
        logTimezone = "UTC",
    )

/** The image's own initdb before DB1: libc en_US.utf8. */
private val IMAGE_DEFAULTS = libc("en_US.utf8", "en_US.utf8")

private fun libc(
    collate: String,
    ctype: String,
) = NEW_INSTALLATION.copy(localeProvider = "c", collate = collate, ctype = ctype, locale = null)

/** Rules "Сервер видит базу, созданную не так, как новая установка" (DB1): what differs, and which version refuses. */
@MutFlowTest
class DatabaseSettingsTest {
    private fun mismatches(settings: DatabaseSettings) = MutFlow.underTest { databaseMismatches(settings) }

    @Test
    fun `База новой установки ничем не отличается`() {
        assertEquals(emptySet(), mismatches(NEW_INSTALLATION))
    }

    @Test
    fun `Выключенные контрольные суммы страниц отличаются`() {
        assertEquals(setOf(DatabaseMismatch.DATA_CHECKSUMS), mismatches(NEW_INSTALLATION.copy(dataChecksums = false)))
    }

    @Test
    fun `Кодировка не UTF8 отличается`() {
        assertEquals(setOf(DatabaseMismatch.ENCODING), mismatches(NEW_INSTALLATION.copy(encoding = "SQL_ASCII")))
    }

    @Test
    fun `Локаль образа по умолчанию отличается`() {
        assertEquals(setOf(DatabaseMismatch.LOCALE), mismatches(IMAGE_DEFAULTS))
    }

    @Test
    fun `Локаль libc C UTF-8 внешней базы не отличается`() {
        assertEquals(emptySet(), mismatches(libc("C.UTF-8", "C.utf8")))
    }

    @Test
    fun `Локаль libc с одной лишь C UTF-8 сортировкой отличается`() {
        val ctype = libc("C.UTF-8", "en_US.utf8")
        val collate = libc("en_US.utf8", "C.UTF-8")

        assertEquals(setOf(DatabaseMismatch.LOCALE), mismatches(ctype))
        assertEquals(setOf(DatabaseMismatch.LOCALE), mismatches(collate))
    }

    @Test
    fun `Встроенная локаль C без UTF-8 и локаль ICU отличаются`() {
        val builtinC = NEW_INSTALLATION.copy(locale = "C")
        val icu = NEW_INSTALLATION.copy(localeProvider = "i", locale = "und")

        assertEquals(setOf(DatabaseMismatch.LOCALE), mismatches(builtinC))
        assertEquals(setOf(DatabaseMismatch.LOCALE), mismatches(icu))
    }

    @Test
    fun `Часовой пояс не UTC отличается, Etc UTC — нет`() {
        assertEquals(setOf(DatabaseMismatch.TIMEZONE), mismatches(NEW_INSTALLATION.copy(logTimezone = "Europe/Berlin")))
        assertEquals(emptySet(), mismatches(NEW_INSTALLATION.copy(logTimezone = "Etc/UTC")))
    }

    @Test
    fun `PostgreSQL новее проверенного отличается, 16 и 17 — нет`() {
        val newer = NEW_INSTALLATION.copy(versionNum = 190000, version = "19.0")

        assertEquals(setOf(DatabaseMismatch.VERSION), mismatches(newer))
        assertEquals(emptySet(), mismatches(NEW_INSTALLATION.copy(versionNum = 160000, version = "16.0")))
        assertEquals(emptySet(), mismatches(NEW_INSTALLATION.copy(versionNum = 179999, version = "17.9")))
    }

    @Test
    fun `Все отличия видны вместе`() {
        val everything =
            IMAGE_DEFAULTS.copy(dataChecksums = false, encoding = "LATIN1", logTimezone = "GMT", versionNum = 190001)

        assertEquals(DatabaseMismatch.entries.toSet(), mismatches(everything))
    }

    @Test
    fun `PostgreSQL 15 отклоняется с версией и страницей документации`() {
        val old = NEW_INSTALLATION.copy(versionNum = 150014, version = "15.14")

        val refusal =
            assertFailsWith<DatabaseVersionUnsupported> { MutFlow.underTest { refuseUnsupportedVersion(old) } }

        val message = checkNotNull(refusal.message)
        val prefix = "Database startup refused: DATABASE_VERSION_UNSUPPORTED: PostgreSQL 15.14"
        assertTrue(message.startsWith(prefix), message)
        assertTrue("16" in message && "docs/operator/16-postgresql.md" in message, message)
    }

    @Test
    fun `PostgreSQL 16 не отклоняется`() {
        val oldest = NEW_INSTALLATION.copy(versionNum = 160000, version = "16.0")

        MutFlow.underTest { refuseUnsupportedVersion(oldest) }
    }

    @Test
    fun `Предупреждение называет каждое отличие, его значение и страницу документации`() {
        val settings = IMAGE_DEFAULTS.copy(dataChecksums = false, logTimezone = "Europe/Berlin")

        val text = MutFlow.underTest { databaseWarning(settings, databaseMismatches(settings)) }

        val expected =
            listOf("data_checksums=off (want on)", "locale=libc en_US.utf8/en_US.utf8 (want C.UTF-8)") +
                listOf("timezone=Europe/Berlin (want UTC)", "docs/operator/16-postgresql.md")
        for (part in expected) assertTrue(part in text, "$part not in $text")
    }

    @Test
    fun `Предупреждение о кодировке, встроенной локали и версии называет их значения`() {
        val settings = NEW_INSTALLATION.copy(encoding = "LATIN1", locale = "C", versionNum = 190002, version = "19.2")

        val text = MutFlow.underTest { databaseWarning(settings, databaseMismatches(settings)) }

        val expected =
            listOf("encoding=LATIN1 (want UTF8)", "locale=builtin C (want C.UTF-8)") +
                listOf("version=19.2 (tested up to 18)")
        for (part in expected) assertTrue(part in text, "$part not in $text")
    }
}
