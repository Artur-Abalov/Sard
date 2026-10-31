// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.pki.MovableClock
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull

private val T0: Instant = Instant.parse("2026-10-30T12:00:00Z")
private val TENANT: UUID = UUID.fromString("00000000-0000-0000-0000-000000000001")

/** Rule "Окончание сессии по сроку публикует событие «сессия истекла»" (server-core-extensions.feature). */
class SessionExpiryTest {
    private val clock = MovableClock(T0)
    private val expired = mutableListOf<Pair<AdminSession, String>>()
    private val store = SessionStore(clock) { session, reason -> expired += session to reason }

    private fun signedIn(address: String = "203.0.113.10") = store.create(TENANT, address)

    @Test
    fun `Сессия без запросов час и минуту даёт сессия истекла с причиной idle`() {
        val session = signedIn()

        clock.now = T0 + Duration.ofMinutes(61)
        store.sweep()

        assertEquals(1, expired.size)
        assertEquals(session.id, expired.single().first.id)
        assertEquals("idle", expired.single().second)
        assertEquals("203.0.113.10", expired.single().first.clientAddress)
    }

    @Test
    fun `Сессия, использованная за минуту до срока бездействия, события не даёт`() {
        val session = signedIn()
        clock.now = T0 + Duration.ofMinutes(59)

        assertNotNull(store.touch(session.id, "203.0.113.10"))
        store.sweep()

        assertEquals(emptyList(), expired)
    }

    @Test
    fun `Активная сессия через восемь часов с входа даёт сессия истекла с причиной absolute`() {
        val session = signedIn()
        var now = T0
        while (now < T0 + Duration.ofHours(8)) {
            now += Duration.ofMinutes(30)
            clock.now = now.coerceAtMost(T0 + Duration.ofHours(7) + Duration.ofMinutes(59))
            store.touch(session.id, "198.51.100.${now.epochSecond % 200}")
        }
        val lastAddress = store.find(session.id)?.clientAddress

        clock.now = T0 + Duration.ofMinutes(8 * 60 + 1)
        store.sweep()

        assertEquals(listOf("absolute"), expired.map { it.second })
        assertEquals(lastAddress, expired.single().first.clientAddress)
    }

    @Test
    fun `Запрос с истёкшей сессией не даёт второго события`() {
        val session = signedIn()
        clock.now = T0 + Duration.ofMinutes(61)

        assertNull(store.touch(session.id, "203.0.113.11"))
        store.sweep()
        assertNull(store.touch(session.id, "203.0.113.11"))

        assertEquals(listOf("idle"), expired.map { it.second })
    }

    @Test
    fun `Выход не даёт события сессия истекла`() {
        val session = signedIn()

        store.remove(session.id)
        clock.now = T0 + Duration.ofHours(2)
        store.sweep()

        assertEquals(emptyList(), expired)
    }

    @Test
    fun `Адрес последнего запроса с сессией попадает в событие`() {
        val session = signedIn("203.0.113.10")
        clock.now = T0 + Duration.ofMinutes(10)
        store.touch(session.id, "198.51.100.7")

        clock.now = T0 + Duration.ofHours(2)
        store.sweep()

        assertEquals("198.51.100.7", expired.single().first.clientAddress)
    }
}
