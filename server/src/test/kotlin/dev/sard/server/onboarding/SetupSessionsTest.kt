// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.pki.MovableClock
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val T0: Instant = Instant.parse("2026-10-09T12:00:00Z")
private val EXPIRY: Instant = Instant.parse("2026-10-10T12:00:00Z")

/** Rule "Верный код выдаёт сессию настройки, и она открывает только мастер" (@service part, Р3). */
class SetupSessionsTest {
    private val clock = MovableClock(T0)
    private val sessions = SetupSessions(clock)

    @Test
    fun `Тысяча сессий различны и несут не менее 128 бит`() {
        val ids = List(1000) { sessions.create(EXPIRY) }

        assertEquals(1000, ids.toSet().size)
        assertTrue(ids.all { it.length * 4 >= 128 && it.all { c -> c in "0123456789abcdef" } })
    }

    @Test
    fun `Сессия действует до срока кода и не дольше`() {
        val id = sessions.create(EXPIRY)

        clock.now = Instant.parse("2026-10-10T11:59:59.999Z")
        assertTrue(sessions.valid(id))
        clock.now = EXPIRY
        assertFalse(sessions.valid(id))
    }

    @Test
    fun `Неизвестный и пустой идентификатор не действуют`() {
        assertFalse(sessions.valid("attacker-chosen"))
        assertFalse(sessions.valid(null))
    }

    @Test
    fun `Завершённая сессия не действует, остальные действуют`() {
        val first = sessions.create(EXPIRY)
        val second = sessions.create(EXPIRY)

        sessions.end(first)

        assertFalse(sessions.valid(first))
        assertTrue(sessions.valid(second))
    }

    @Test
    fun `Все сессии завершаются разом`() {
        val ids = List(3) { sessions.create(EXPIRY) }

        sessions.endAll()

        assertTrue(ids.none { sessions.valid(it) })
    }

    @Test
    fun `Истёкшие сессии вычищаются при создании новой`() {
        val old = sessions.create(EXPIRY)
        clock.now = EXPIRY

        sessions.create(EXPIRY + java.time.Duration.ofHours(1))

        assertEquals(1, sessions.tracked())
        assertFalse(sessions.valid(old))
    }
}
