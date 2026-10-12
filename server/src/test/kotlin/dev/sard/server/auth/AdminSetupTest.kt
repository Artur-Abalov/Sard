// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.pki.MovableClock
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

/** The admin step of the open core and of an extension that signs people in itself (Р16). */
@MutFlowTest
class AdminSetupTest {
    private val administrators = FakeAdministrators()
    private val hasher = PasswordHasher()
    private val setup = StoredAdminSetup(administrators, hasher, MovableClock(Instant.parse("2026-10-09T12:00:00Z")))

    @Test
    fun `Шаг admin открыт, пока администратора нет`() {
        assertFalse(MutFlow.underTest { setup.done() })
        assertFalse(setup.external)
    }

    @Test
    fun `Пароль создаёт администратора, и шаг admin выполнен`() {
        assertTrue(MutFlow.underTest { setup.create("correct-horse-battery") })

        assertTrue(setup.done())
        assertTrue(hasher.matches("correct-horse-battery", checkNotNull(administrators.stored)))
    }

    @Test
    fun `Второй пароль администратора не создаёт и прежний хэш не меняет`() {
        setup.create("correct-horse-battery")
        val stored = administrators.stored

        assertFalse(setup.create("another-password-1"))

        assertEquals(stored, administrators.stored)
    }

    @Test
    fun `Хэш выбирает соль заново, в базе нет самого пароля`() {
        setup.create("correct-horse-battery")

        assertNotEquals("correct-horse-battery", administrators.stored)
    }

    @Test
    fun `Вход через расширение — шаг admin всегда выполнен, пароль не создаётся`() {
        assertTrue(ExternalAdminSetup.done())
        assertTrue(ExternalAdminSetup.external)
        assertFalse(ExternalAdminSetup.create("correct-horse-battery"))
    }
}
