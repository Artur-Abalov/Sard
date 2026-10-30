// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.pki.MovableClock
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
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

@MutFlowTest
class SessionStoreTest {
    private val clock = MovableClock(LOGIN)
    private val store = SessionStore(clock)

    private fun touch(id: String) = MutFlow.underTest { store.touch(id) }

    private fun find(id: String) = MutFlow.underTest { store.find(id) }

    private fun expiresAt(session: AdminSession) = MutFlow.underTest { store.expiresAt(session) }

    @Test
    fun `a session created just now is valid`() {
        val session = store.create(TENANT)
        assertEquals(TENANT, session.tenantId)
        assertNotNull(touch(session.id))
    }

    @Test
    fun `an unknown session id is not valid`() {
        assertNull(touch("no-such-session"))
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
    fun `a session a millisecond before 1 hour idle is still valid`() {
        val session = store.create(TENANT)
        clock.now = LOGIN + Duration.ofHours(1) - Duration.ofMillis(1)
        assertNotNull(touch(session.id))
    }

    @Test
    fun `a session exactly 1 hour idle is no longer valid`() {
        val session = store.create(TENANT)
        clock.now = LOGIN + Duration.ofHours(1)
        assertNull(touch(session.id))
    }

    @Test
    fun `touching a session extends its idle deadline from the touch time`() {
        val session = store.create(TENANT)
        val touchTime = LOGIN + Duration.ofMinutes(30)
        clock.now = touchTime
        assertNotNull(touch(session.id))
        clock.now = touchTime + Duration.ofHours(1) - Duration.ofMillis(1)
        assertNotNull(touch(session.id))
    }

    @Test
    fun `a session does not outlive 8 hours from login however often it is touched`() {
        val session = store.create(TENANT)
        var now = LOGIN
        while (now + Duration.ofMinutes(30) < LOGIN + Duration.ofHours(8)) {
            now += Duration.ofMinutes(30)
            clock.now = now
            assertNotNull(touch(session.id), "expired too early at $now")
        }
        clock.now = LOGIN + Duration.ofHours(8)
        assertNull(touch(session.id))
    }

    @Test
    fun `expiresAt is the idle deadline capped at 8 hours from login`() {
        val session = store.create(TENANT)
        var now = LOGIN
        while (now + Duration.ofMinutes(30) < LOGIN + Duration.ofHours(8)) {
            now += Duration.ofMinutes(30)
            clock.now = now
            assertNotNull(touch(session.id))
        }
        val touched = assertNotNull(touch(session.id))
        assertEquals(LOGIN + Duration.ofHours(8), expiresAt(touched))
    }

    @Test
    fun `expiresAt without the cap is the touch time plus 1 hour`() {
        val session = store.create(TENANT)
        clock.now = LOGIN + Duration.ofMinutes(10)
        val touched = touch(session.id)
        assertEquals(clock.now + Duration.ofHours(1), touched?.let { expiresAt(it) })
    }

    @Test
    fun `removing a session ends it`() {
        val session = store.create(TENANT)
        assertTrue(store.remove(session.id))
        assertNull(touch(session.id))
    }

    @Test
    fun `removing an unknown session id is a no-op`() {
        assertFalse(store.remove("no-such-session"))
    }

    @Test
    fun `find does not extend the idle deadline, unlike touch`() {
        val session = store.create(TENANT)
        clock.now = LOGIN + Duration.ofMinutes(30)
        assertNotNull(find(session.id))
        clock.now = LOGIN + Duration.ofHours(1)
        assertNull(find(session.id))
    }

    @Test
    fun `find returns the session unchanged`() {
        val session = store.create(TENANT)
        clock.now = LOGIN + Duration.ofMinutes(10)
        assertEquals(session, find(session.id))
    }

    @Test
    fun `find of an unknown session id is null`() {
        assertNull(find("no-such-session"))
    }

    @Test
    fun `two logins give two sessions valid at once with different ids`() {
        val a = store.create(TENANT)
        val b = store.create(TENANT)
        assertNotEquals(a.id, b.id)
        assertNotNull(touch(a.id))
        assertNotNull(touch(b.id))
    }
}
