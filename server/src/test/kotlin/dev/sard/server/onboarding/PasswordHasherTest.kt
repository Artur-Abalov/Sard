// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

/** Rule "Пароль хранится только как хэш Argon2id" (@service, Р8, OQ-193). */
class PasswordHasherTest {
    private val hasher = PasswordHasher()

    @Test
    fun `Хэш пароля — строка PHC Argon2id с заданными параметрами`() {
        val hash = hasher.hash("correct-horse-battery")

        assertTrue(hash.startsWith("\$argon2id\$v=19\$m=19456,t=2,p=1\$"), hash)
        assertFalse("correct-horse-battery" in hash)
    }

    @Test
    fun `Один и тот же пароль даёт разные хэши и каждый проверяется`() {
        val first = hasher.hash("correct-horse-battery")
        val second = hasher.hash("correct-horse-battery")

        assertNotEquals(first, second)
        assertTrue(hasher.matches("correct-horse-battery", first))
        assertTrue(hasher.matches("correct-horse-battery", second))
    }

    @Test
    fun `Другой пароль не проверяется`() {
        val hash = hasher.hash("correct-horse-battery")

        assertFalse(hasher.matches("Correct-Horse-Battery", hash))
        assertFalse(hasher.matches("correct-horse-battery ", hash))
    }

    @Test
    fun `Повреждённый хэш в базе не совпадает ни с каким паролем`() {
        assertFalse(hasher.matches("correct-horse-battery", "not-a-hash"))
    }

    @Test
    fun `Соль 16 байт и хэш 32 байта`() {
        val parts = hasher.hash("correct-horse-battery").split('$')

        assertEquals(22, parts[4].length) // 16 bytes in unpadded base64
        assertEquals(43, parts[5].length) // 32 bytes in unpadded base64
    }
}
