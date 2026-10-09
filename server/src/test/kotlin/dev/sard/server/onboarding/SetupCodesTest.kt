// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.SetupCodeState
import dev.sard.server.pki.MovableClock
import java.lang.reflect.Modifier
import java.time.Duration
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val ALPHABET = Regex("[0-9A-HJKMNP-TV-Z]{28}")

/** Rules "Сервер без администратора печатает код настройки при старте", "Код действует до шага admin" (@service). */
class SetupCodesTest {
    private val clock = MovableClock(T0)
    private val codes = SetupCodes(clock) { CODE }

    @Test
    fun `Тысяча кодов различны и несут 140 бит`() {
        val generator = RandomSetupCodeGenerator()

        val issued = List(1000) { generator.generate() }

        assertEquals(1000, issued.toSet().size)
        for (code in issued) {
            assertTrue(ALPHABET.matches(code.replace("-", "")), code)
            assertEquals(7, code.split('-').size)
            assertTrue(code.split('-').all { it.length == 4 }, code)
        }
    }

    @Test
    fun `Выданный код действует 24 часа ровно`() {
        val issued = codes.issue()

        assertEquals(CODE, issued.display)
        assertEquals(Instant.parse("2026-10-10T12:00:00Z"), issued.expiresAt)
        clock.now = Instant.parse("2026-10-10T11:59:59.999Z")
        assertTrue(codes.accepts(CODE))
        assertEquals(SetupCodeState.ACTIVE, codes.state())
        clock.now = Instant.parse("2026-10-10T12:00:00Z")
        assertFalse(codes.accepts(CODE))
        assertEquals(SetupCodeState.EXPIRED, codes.state())
    }

    @Test
    fun `Без выдачи кода его нет`() {
        assertEquals(SetupCodeState.NOT_ISSUED, codes.state())
        assertFalse(codes.accepts(CODE))
        assertNull(codes.expiresAt())
    }

    @Test
    fun `После закрытия код не принимается и состояние not_issued`() {
        codes.issue()

        codes.close()

        assertFalse(codes.accepts(CODE))
        assertEquals(SetupCodeState.NOT_ISSUED, codes.state())
    }

    @Test
    fun `Новая выдача заменяет прежний код`() {
        var next = CODE
        val rotating = SetupCodes(clock) { next }
        rotating.issue()
        next = "0000-1111-2222-3333-4444-5555-6666"

        rotating.issue()

        assertFalse(rotating.accepts(CODE))
        assertTrue(rotating.accepts(next))
    }

    @Test
    fun `Допустимые написания кода принимаются`() {
        codes.issue()
        val spellings =
            listOf(
                "abcd-efgh-jkmn-pqrs-tvwx-yz01-2345",
                "ABCDEFGHJKMNPQRSTVWXYZ012345",
                "ABCD EFGH JKMN PQRS TVWX YZ01 2345",
                "  ABCD-EFGH-JKMN-PQRS-TVWX-YZ01-2345\n",
                "ABCD-EFGH-JKMN-PQRS-TVWX-YZO1-2345",
                "ABCD-EFGH-JKMN-PQRS-TVWX-YZ0I-2345",
                "ABCD-EFGH-JKMN-PQRS-TVWX-YZ0l-2345",
            )

        for (spelling in spellings) assertTrue(codes.accepts(spelling), spelling)
    }

    @Test
    fun `Неверные написания не принимаются`() {
        codes.issue()
        val wrong =
            listOf(
                null,
                "",
                "ABCD",
                "ABCD-EFGH-JKMN-PQRS-TVWX-YZ01-2346",
                "ABCD-EFGH-JKMN-PQRS-TVWX-YZ01",
                "$CODE-0000",
                "UBCD-EFGH-JKMN-PQRS-TVWX-YZ01-2345",
                "A".repeat(10_000),
            )

        for (input in wrong) assertFalse(codes.accepts(input), "$input".take(40))
    }

    @Test
    fun `Объект не хранит сам код — только SHA-256 нормализованного`() {
        codes.issue()
        val fields = SetupCodes::class.java.declaredFields.filterNot { Modifier.isStatic(it.modifiers) }

        val held =
            fields.flatMap { field ->
                field.isAccessible = true
                listOf(field.get(codes)).flatMap { value -> leaves(value) }
            }

        assertTrue(held.none { CODE in it || CODE.replace("-", "") in it }, held.toString())
        assertTrue(held.any { it == sha256Hex(CODE.replace("-", "")) }, held.toString())
    }

    /** Every string a value reaches: text, ASCII bytes, hex of bytes and the fields of nested holders. */
    private fun leaves(value: Any?): List<String> =
        when (value) {
            null -> {
                emptyList()
            }

            is String -> {
                listOf(value)
            }

            is ByteArray -> {
                listOf(String(value, Charsets.ISO_8859_1), value.joinToString("") { "%02x".format(it) })
            }

            is Instant, is Duration, is MovableClock, is Function<*>, is SetupCodeGenerator -> {
                emptyList()
            }

            else -> {
                value.javaClass.declaredFields
                    .filterNot { Modifier.isStatic(it.modifiers) }
                    .flatMap {
                        it.isAccessible = true
                        leaves(it.get(value))
                    }
            }
        }

    private fun sha256Hex(text: String) =
        java.security.MessageDigest
            .getInstance("SHA-256")
            .digest(text.toByteArray())
            .joinToString("") { "%02x".format(it) }

    @Test
    fun `Нормализация даёт 28 знаков или ничего`() {
        assertEquals("ABCDEFGHJKMNPQRSTVWXYZ012345", SetupCodes.normalize(CODE.lowercase()))
        assertNotEquals(null, SetupCodes.normalize("0".repeat(28)))
        assertNull(SetupCodes.normalize("0".repeat(27)))
        assertNull(SetupCodes.normalize("0".repeat(29)))
    }
}
