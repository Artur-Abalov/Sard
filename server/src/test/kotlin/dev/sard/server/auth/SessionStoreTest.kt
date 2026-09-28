// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.pki.MovableClock
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val LOGIN: Instant = Instant.parse("2026-10-01T12:00:00Z")
private val TENANT: UUID = UUID.fromString("00000000-0000-0000-0000-000000000001")

class SessionStoreTest {
    private val clock = MovableClock(LOGIN)
    private val store = SessionStore(clock)

    @Test
    fun `a session created just now is valid`() {
        val session = store.create(TENANT)
        assertEquals(TENANT, session.tenantId)
        assertNotNull(store.touch(session.id))
    }

    @Test
    fun `an unknown session id is not valid`() {
        assertNull(store.touch("no-such-session"))
    }

    @Test
    fun `two sessions never share an id`() {
        val ids = (1..1000).map { store.create(TENANT).id }
        assertEquals(1000, ids.toSet().size)
    }

    @Test
    fun `a session id carries at least 128 bits of randomness by its length and alphabet`() {
        val id = store.create(TENANT).id
        val alphabetSize = id.toSet().let { chars -> maxOf(chars.size, 16) }
        val bits = id.length * (Math.log(alphabetSize.toDouble()) / Math.log(2.0))
        assertTrue(bits >= 128, "only $bits bits in '$id'")
    }

    @Test
    fun `a session a millisecond before 12 hours idle is still valid`() {
        val session = store.create(TENANT)
        clock.now = LOGIN + Duration.ofHours(12) - Duration.ofMillis(1)
        assertNotNull(store.touch(session.id))
    }

    @Test
    fun `a session exactly 12 hours idle is no longer valid`() {
        val session = store.create(TENANT)
        clock.now = LOGIN + Duration.ofHours(12)
        assertNull(store.touch(session.id))
    }

    @Test
    fun `touching a session extends its idle deadline from the touch time`() {
        val session = store.create(TENANT)
        val touchTime = LOGIN + Duration.ofHours(11)
        clock.now = touchTime
        assertNotNull(store.touch(session.id))
        clock.now = touchTime + Duration.ofHours(12) - Duration.ofMillis(1)
        assertNotNull(store.touch(session.id))
    }

    @Test
    fun `a session does not outlive 7 days from login however often it is touched`() {
        val session = store.create(TENANT)
        var now = LOGIN
        while (now + Duration.ofHours(11) < LOGIN + Duration.ofDays(7)) {
            now += Duration.ofHours(11)
            clock.now = now
            assertNotNull(store.touch(session.id), "expired too early at $now")
        }
        clock.now = LOGIN + Duration.ofDays(7)
        assertNull(store.touch(session.id))
    }

    @Test
    fun `expiresAt is the idle deadline capped at 7 days from login`() {
        val session = store.create(TENANT)
        var now = LOGIN
        while (now + Duration.ofHours(11) < LOGIN + Duration.ofDays(7)) {
            now += Duration.ofHours(11)
            clock.now = now
            assertNotNull(store.touch(session.id))
        }
        val touched = assertNotNull(store.touch(session.id))
        assertEquals(LOGIN + Duration.ofDays(7), store.expiresAt(touched))
    }

    @Test
    fun `expiresAt without the cap is the touch time plus 12 hours`() {
        val session = store.create(TENANT)
        clock.now = LOGIN + Duration.ofHours(1)
        val touched = store.touch(session.id)
        assertEquals(clock.now + Duration.ofHours(12), touched?.let { store.expiresAt(it) })
    }

    @Test
    fun `removing a session ends it`() {
        val session = store.create(TENANT)
        assertTrue(store.remove(session.id))
        assertNull(store.touch(session.id))
    }

    @Test
    fun `removing an unknown session id is a no-op`() {
        assertFalse(store.remove("no-such-session"))
    }

    @Test
    fun `two logins give two sessions valid at once with different ids`() {
        val a = store.create(TENANT)
        val b = store.create(TENANT)
        assertNotEquals(a.id, b.id)
        assertNotNull(store.touch(a.id))
        assertNotNull(store.touch(b.id))
    }
}
