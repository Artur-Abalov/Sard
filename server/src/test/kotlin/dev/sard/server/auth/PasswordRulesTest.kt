// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** Р7: 12 to 1024 Unicode code points inclusive, no trimming, no normalisation. */
@MutFlowTest
class PasswordRulesTest {
    private fun acceptable(password: String) = MutFlow.underTest { PasswordRules.acceptable(password) }

    @Test
    fun `Пустой пароль и пароль из 11 знаков не годятся`() {
        assertFalse(acceptable(""))
        assertFalse(acceptable("short-pw-11"))
    }

    @Test
    fun `Пароль из 12 знаков годится`() {
        assertTrue(acceptable("exactly-12ch"))
    }

    @Test
    fun `Длина считается в кодовых точках, а не в байтах`() {
        assertFalse(acceptable("парольнекор"))
        assertTrue(acceptable("паролькирилл"))
    }

    @Test
    fun `Длина считается в кодовых точках, а не в единицах UTF-16`() {
        assertTrue(acceptable("🔑".repeat(12)))
        assertFalse(acceptable("🔑".repeat(11)))
    }

    @Test
    fun `Пароль из 1024 знаков годится, из 1025 нет`() {
        assertTrue(acceptable("a".repeat(1024)))
        assertFalse(acceptable("a".repeat(1025)))
    }

    @Test
    fun `Пробелы не обрезаются`() {
        assertFalse(acceptable("   short   "))
        assertTrue(acceptable("correct-horse-battery "))
    }
}
