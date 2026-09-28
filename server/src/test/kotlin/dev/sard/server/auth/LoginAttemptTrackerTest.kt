// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.pki.MovableClock
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.Instant
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val NOW: Instant = Instant.parse("2026-10-01T12:00:00Z")
private const val ADDRESS = "203.0.113.10"
private const val OTHER_ADDRESS = "198.51.100.7"

@MutFlowTest
class LoginAttemptTrackerTest {
    private val clock = MovableClock(NOW)
    private val tracker = LoginAttemptTracker(clock)

    private fun fail(times: Int) = repeat(times) { MutFlow.underTest { tracker.recordFailure(ADDRESS) } }

    private fun retryAfterSeconds(address: String = ADDRESS) = MutFlow.underTest { tracker.retryAfterSeconds(address) }

    @Test
    fun `a fresh address is not locked`() {
        assertNull(retryAfterSeconds())
    }

    @Test
    fun `four failures do not lock`() {
        fail(4)
        assertNull(retryAfterSeconds())
    }

    @Test
    fun `the fifth failure is the one that starts the lock`() {
        assertFalse(
            run {
                fail(3)
                MutFlow.underTest { tracker.recordFailure(ADDRESS) }
            },
        )
        assertTrue(MutFlow.underTest { tracker.recordFailure(ADDRESS) })
        assertFalse(MutFlow.underTest { tracker.recordFailure(ADDRESS) })
    }

    @Test
    fun `retryAfterSeconds is 900 right after the fifth failure`() {
        fail(5)
        assertEquals(900L, retryAfterSeconds())
    }

    @Test
    fun `retryAfterSeconds counts down`() {
        fail(5)
        clock.now = NOW + Duration.ofMinutes(10)
        assertEquals(300L, retryAfterSeconds())
    }

    @Test
    fun `retryAfterSeconds rounds up to a whole second`() {
        fail(5)
        clock.now = NOW + Duration.ofMinutes(14) + Duration.ofSeconds(59) + Duration.ofMillis(999)
        assertEquals(1L, retryAfterSeconds())
    }

    @Test
    fun `the lock lifts exactly 15 minutes after the fifth failure`() {
        fail(5)
        clock.now = NOW + Duration.ofMinutes(15)
        assertNull(retryAfterSeconds())
    }

    @Test
    fun `a lock that has lifted can be re-triggered by 5 fresh failures`() {
        fail(5)
        clock.now = NOW + Duration.ofMinutes(15)
        assertNull(retryAfterSeconds())
        assertTrue(fail5AfterLift())
    }

    private fun fail5AfterLift(): Boolean {
        repeat(4) { MutFlow.underTest { tracker.recordFailure(ADDRESS) } }
        return MutFlow.underTest { tracker.recordFailure(ADDRESS) }
    }

    @Test
    fun `failed attempts during the lock do not extend it`() {
        fail(5)
        clock.now = NOW + Duration.ofMinutes(1)
        MutFlow.underTest { tracker.recordFailure(ADDRESS) }
        clock.now = NOW + Duration.ofMinutes(5)
        MutFlow.underTest { tracker.recordFailure(ADDRESS) }
        clock.now = NOW + Duration.ofMinutes(14)
        MutFlow.underTest { tracker.recordFailure(ADDRESS) }
        clock.now = NOW + Duration.ofMinutes(15)
        assertNull(retryAfterSeconds())
    }

    @Test
    fun `retryAfterSeconds rounds up even when whole seconds remain, not just at the boundary`() {
        fail(5)
        clock.now = NOW + Duration.ofMinutes(10) + Duration.ofMillis(1)
        assertEquals(300L, retryAfterSeconds())
    }

    @Test
    fun `retryAfterSeconds rounds up on a single remaining nanosecond, not just deep fractions`() {
        fail(5)
        clock.now = NOW + Duration.ofMinutes(10).plusSeconds(1).minusNanos(1)
        assertEquals(300L, retryAfterSeconds())
    }

    @Test
    fun `failures older than 15 minutes drop out of the window`() {
        fail(4)
        clock.now = NOW + Duration.ofMinutes(15)
        fail(4)
        assertNull(retryAfterSeconds())
    }

    @Test
    fun `a success clears the failure counter`() {
        fail(4)
        tracker.recordSuccess(ADDRESS)
        fail(4)
        assertNull(retryAfterSeconds())
    }

    @Test
    fun `locking one address does not affect another`() {
        fail(5)
        assertNull(retryAfterSeconds(OTHER_ADDRESS))
    }

    @Test
    fun `20 concurrent failures lock after at most 5`() {
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(20)
        val locks =
            (1..20).map {
                pool.submit<Boolean> {
                    start.await()
                    tracker.recordFailure(ADDRESS)
                }
            }
        start.countDown()
        val results = locks.map { it.get(10, TimeUnit.SECONDS) }
        pool.shutdown()
        assertEquals(1, results.count { it })
    }
}
