// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.events.SardEventPublisher
import dev.sard.server.events.SettingsEvents
import dev.sard.server.events.SubjectActors
import dev.sard.server.extension.SardEvent
import dev.sard.server.extension.SettingsChanged
import dev.sard.server.extension.TenantResolver
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
    private val clock = MovableClock(Instant.parse("2026-10-09T12:00:00Z"))
    private val events = mutableListOf<SardEvent>()
    private val publisher = SardEventPublisher { events += it as SardEvent }
    private val tenant = TenantResolver.DEFAULT_TENANT_ID
    private val setup =
        StoredAdminSetup(administrators, hasher, clock, SettingsEvents(publisher, SubjectActors.NONE, clock) { tenant })

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

    @Test
    fun `Пароль в мастере первого запуска даёт settings changed без субъекта и без входа`() {
        assertTrue(MutFlow.underTest { setup.create("correct-horse-battery") })
        assertEquals(1, events.size)
        assertFalse(MutFlow.underTest { setup.create("another-password-1") })

        assertEquals(listOf<SardEvent>(SettingsChanged(clock.instant(), tenant, null, "password", null)), events)
    }
}
