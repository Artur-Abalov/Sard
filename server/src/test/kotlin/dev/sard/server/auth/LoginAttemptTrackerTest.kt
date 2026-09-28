// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.pki.MovableClock
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

class LoginAttemptTrackerTest {
    private val clock = MovableClock(NOW)
    private val tracker = LoginAttemptTracker(clock)

    private fun fail(times: Int) = repeat(times) { tracker.recordFailure(ADDRESS) }

    @Test
    fun `a fresh address is not locked`() {
        assertNull(tracker.retryAfterSeconds(ADDRESS))
    }

    @Test
    fun `four failures do not lock`() {
        fail(4)
        assertNull(tracker.retryAfterSeconds(ADDRESS))
    }

    @Test
    fun `the fifth failure is the one that starts the lock`() {
        assertFalse(
            run {
                fail(3)
                tracker.recordFailure(ADDRESS)
            },
        )
        assertTrue(tracker.recordFailure(ADDRESS))
        assertFalse(tracker.recordFailure(ADDRESS))
    }

    @Test
    fun `retryAfterSeconds is 900 right after the fifth failure`() {
        fail(5)
        assertEquals(900L, tracker.retryAfterSeconds(ADDRESS))
    }

    @Test
    fun `retryAfterSeconds counts down`() {
        fail(5)
        clock.now = NOW + Duration.ofMinutes(10)
        assertEquals(300L, tracker.retryAfterSeconds(ADDRESS))
    }

    @Test
    fun `retryAfterSeconds rounds up to a whole second`() {
        fail(5)
        clock.now = NOW + Duration.ofMinutes(14) + Duration.ofSeconds(59) + Duration.ofMillis(999)
        assertEquals(1L, tracker.retryAfterSeconds(ADDRESS))
    }

    @Test
    fun `the lock lifts exactly 15 minutes after the fifth failure`() {
        fail(5)
        clock.now = NOW + Duration.ofMinutes(15)
        assertNull(tracker.retryAfterSeconds(ADDRESS))
    }

    @Test
    fun `failed attempts during the lock do not extend it`() {
        fail(5)
        clock.now = NOW + Duration.ofMinutes(1)
        tracker.recordFailure(ADDRESS)
        clock.now = NOW + Duration.ofMinutes(5)
        tracker.recordFailure(ADDRESS)
        clock.now = NOW + Duration.ofMinutes(14)
        tracker.recordFailure(ADDRESS)
        clock.now = NOW + Duration.ofMinutes(15)
        assertNull(tracker.retryAfterSeconds(ADDRESS))
    }

    @Test
    fun `failures older than 15 minutes drop out of the window`() {
        fail(4)
        clock.now = NOW + Duration.ofMinutes(15)
        fail(4)
        assertNull(tracker.retryAfterSeconds(ADDRESS))
    }

    @Test
    fun `a success clears the failure counter`() {
        fail(4)
        tracker.recordSuccess(ADDRESS)
        fail(4)
        assertNull(tracker.retryAfterSeconds(ADDRESS))
    }

    @Test
    fun `locking one address does not affect another`() {
        fail(5)
        assertNull(tracker.retryAfterSeconds(OTHER_ADDRESS))
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
