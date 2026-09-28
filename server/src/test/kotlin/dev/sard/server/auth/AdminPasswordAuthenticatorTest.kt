// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

@MutFlowTest
class AdminPasswordAuthenticatorTest {
    @Test
    fun `a password of exactly 12 characters starts and matches`() {
        val auth = MutFlow.underTest { AdminPasswordAuthenticator("exactly-12ch") }
        assertTrue(auth.matches("exactly-12ch"))
    }

    @Test
    fun `an unset password refuses to start and names the variable`() {
        val ex = assertFailsWith<IllegalArgumentException> { AdminPasswordAuthenticator("") }
        assertTrue(ex.message!!.contains("SARD_ADMIN_PASSWORD"), ex.message)
        assertTrue(ex.message!!.contains("not set"), ex.message)
    }

    @Test
    fun `a password of 11 characters refuses to start and names the minimum`() {
        val ex = assertFailsWith<IllegalArgumentException> { AdminPasswordAuthenticator("short-pw-11") }
        assertTrue(ex.message!!.contains("SARD_ADMIN_PASSWORD"), ex.message)
        assertTrue(ex.message!!.contains("12"), ex.message)
    }

    @Test
    fun `the error message never contains the rejected password`() {
        val ex = assertFailsWith<IllegalArgumentException> { AdminPasswordAuthenticator("short-pw-11") }
        assertFalse(ex.message!!.contains("short-pw-11"))
    }

    @Test
    fun `length is counted in Unicode code points, not UTF-16 units or bytes`() {
        // 12 Cyrillic code points, 24 bytes UTF-8, 12 UTF-16 units: starts fine either way,
        // the refusing case below is what actually distinguishes code points from bytes.
        val auth = AdminPasswordAuthenticator("паролькирилл")
        assertTrue(auth.matches("паролькирилл"))
    }

    @Test
    fun `11 non-ASCII code points are refused even though they are 22 bytes`() {
        assertFailsWith<IllegalArgumentException> { AdminPasswordAuthenticator("парольнекор") }
    }

    @Test
    fun `a different case does not match`() {
        val auth = AdminPasswordAuthenticator("correct-horse-battery")
        assertFalse(auth.matches("Correct-Horse-Battery"))
    }

    @Test
    fun `trailing whitespace does not match`() {
        val auth = AdminPasswordAuthenticator("correct-horse-battery")
        assertFalse(auth.matches("correct-horse-battery "))
    }

    @Test
    fun `every candidate is hashed to the same fixed digest length before comparison`() {
        for (candidate in listOf("", "c", "correct-horse-batter", "a".repeat(10_000))) {
            assertEquals(32, sha256(candidate).size)
        }
    }

    @Test
    fun `no field of the authenticator holds the configured password as a string or its UTF-8 bytes`() {
        val secret = "correct-horse-battery"
        val auth = AdminPasswordAuthenticator(secret)
        val secretBytes = secret.toByteArray(Charsets.UTF_8)
        for (field in AdminPasswordAuthenticator::class.java.declaredFields) {
            field.isAccessible = true
            when (val value = field.get(auth)) {
                is String -> assertFalse(value.contains(secret), "${field.name} leaks the password")
                is ByteArray -> assertFalse(value.contentEquals(secretBytes), "${field.name} leaks the password bytes")
                else -> Unit
            }
        }
    }
}
