// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.pki.MovableClock
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals

private val NOW: Instant = Instant.parse("2026-10-01T12:00:00Z")

/** Item 6: an address with no failures in the window and no lock is not dead weight in the map. */
@MutFlowTest
class LoginAttemptTrackerMemoryTest {
    private val clock = MovableClock(NOW)
    private val tracker = LoginAttemptTracker(clock)

    @Test
    fun `a successful sign-in leaves no entry for the address`() {
        tracker.withAddressLock("203.0.113.10") { tracker.recordFailure("203.0.113.10") }
        tracker.withAddressLock("203.0.113.10") { tracker.recordSuccess("203.0.113.10") }
        assertEquals(0, MutFlow.underTest { tracker.trackedAddresses() })
    }

    @Test
    fun `failures that age out of the window are forgotten, not just ignored`() {
        tracker.withAddressLock("203.0.113.10") { tracker.recordFailure("203.0.113.10") }
        assertEquals(1, MutFlow.underTest { tracker.trackedAddresses() })
        clock.now = NOW + Duration.ofMinutes(15)
        // Any later attempt from the address sweeps it, even one that never touches this address's state directly.
        tracker.withAddressLock("203.0.113.10") { tracker.retryAfterSeconds("203.0.113.10") }
        assertEquals(0, MutFlow.underTest { tracker.trackedAddresses() })
    }

    @Test
    fun `an unlocked, unexpired lock keeps its entry`() {
        tracker.withAddressLock("203.0.113.10") { repeat(5) { tracker.recordFailure("203.0.113.10") } }
        assertEquals(1, MutFlow.underTest { tracker.trackedAddresses() })
    }

    @Test
    fun `a lock that just expired is forgotten once it is checked`() {
        tracker.withAddressLock("203.0.113.10") { repeat(5) { tracker.recordFailure("203.0.113.10") } }
        clock.now = NOW + Duration.ofMinutes(15)
        tracker.withAddressLock("203.0.113.10") { tracker.retryAfterSeconds("203.0.113.10") }
        assertEquals(0, MutFlow.underTest { tracker.trackedAddresses() })
    }

    @Test
    fun `different addresses do not affect each other's entries`() {
        tracker.withAddressLock("203.0.113.10") { tracker.recordFailure("203.0.113.10") }
        tracker.withAddressLock("198.51.100.7") { tracker.recordSuccess("198.51.100.7") }
        assertEquals(1, MutFlow.underTest { tracker.trackedAddresses() })
    }
}
