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

private val NOW: Instant = Instant.parse("2026-10-01T12:00:00Z")
private val TENANT: UUID = UUID.fromString("00000000-0000-0000-0000-000000000001")

/** Item 7: create() sweeps expired sessions, a cheap, bounded opportunity. */
@MutFlowTest
class SessionStoreMemoryTest {
    private val clock = MovableClock(NOW)
    private val store = SessionStore(clock)

    @Test
    fun `an expired session is gone by the time the next one is created`() {
        store.create(TENANT)
        clock.now = NOW + Duration.ofHours(12)
        assertEquals(1, MutFlow.underTest { store.trackedSessions() })
        store.create(TENANT)
        assertEquals(1, MutFlow.underTest { store.trackedSessions() })
    }

    @Test
    fun `a still-valid session survives the sweep`() {
        val first = store.create(TENANT)
        clock.now = NOW + Duration.ofHours(1)
        store.create(TENANT)
        assertEquals(2, MutFlow.underTest { store.trackedSessions() })
        assertEquals(first.id, store.touch(first.id)?.id)
    }
}
