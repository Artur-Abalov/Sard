// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import org.junit.jupiter.api.Test
import java.time.Duration
import java.time.Instant
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

private val CREATED: Instant = Instant.parse("2026-10-01T10:00:00Z")
private val POLICY = RetryPolicy(RetrySettings())

/** S9a, answer В8: 429 waits retry_after, 5xx and network back off, other 4xx fail, all within the TTL. */
class RetryPolicyTest {
    private fun at(minutes: Long): Instant = CREATED.plus(Duration.ofMinutes(minutes))

    @Test
    fun `a delivered message is delivered`() {
        assertEquals(Decision.Delivered, POLICY.decide(SendOutcome.Delivered, attempts = 0, CREATED, at(1)))
    }

    @Test
    fun `a rejected message fails at once with its reason`() {
        val decision = POLICY.decide(SendOutcome.Rejected("HTTP 400: chat not found"), 0, CREATED, at(1))
        assertEquals(Decision.Failed("HTTP 400: chat not found", attempts = 0), decision)
    }

    @Test
    fun `429 waits retry_after and does not count an attempt`() {
        val decision = POLICY.decide(SendOutcome.RetryAfter(Duration.ofSeconds(37)), 3, CREATED, at(1))
        assertEquals(Decision.Retry(at(1).plusSeconds(37), attempts = 3, "HTTP 429: retry after 37 s"), decision)
    }

    @Test
    fun `retry_after is capped`() {
        val decision = POLICY.decide(SendOutcome.RetryAfter(Duration.ofHours(5)), 0, CREATED, at(1))
        assertEquals(Decision.Retry(at(61), attempts = 0, "HTTP 429: retry after 18000 s"), decision)
    }

    @Test
    fun `transient failures back off exponentially from the initial delay`() {
        val delays =
            (0..4).map { attempts ->
                val outcome = SendOutcome.Transient("HTTP 502")
                val decision = POLICY.decide(outcome, attempts, CREATED, at(1)) as Decision.Retry
                Duration.between(at(1), decision.at).seconds to decision.attempts
            }
        assertEquals(listOf(10L to 1, 20L to 2, 40L to 3, 80L to 4, 160L to 5), delays)
    }

    @Test
    fun `the backoff is capped at the maximum delay`() {
        val decision = POLICY.decide(SendOutcome.Transient("timeout"), 6, CREATED, at(1)) as Decision.Retry
        assertEquals(at(11), decision.at)
    }

    @Test
    fun `the last allowed attempt fails with the count and the reason`() {
        val decision = POLICY.decide(SendOutcome.Transient("HTTP 500"), 7, CREATED, at(1))
        assertEquals(Decision.Failed("gave up after 8 attempts: HTTP 500", attempts = 8), decision)
    }

    @Test
    fun `a retry past the time to live expires instead`() {
        val decision = POLICY.decide(SendOutcome.Transient("HTTP 503"), 4, CREATED, at(24 * 60 - 1))
        assertEquals(Decision.Expired("not delivered within PT24H: HTTP 503", attempts = 5), decision)
    }

    @Test
    fun `a 429 past the time to live expires instead`() {
        val decision = POLICY.decide(SendOutcome.RetryAfter(Duration.ofMinutes(2)), 0, CREATED, at(24 * 60 - 1))
        val expected = Decision.Expired("not delivered within PT24H: HTTP 429: retry after 120 s", attempts = 0)
        assertEquals(expected, decision)
    }

    @Test
    fun `a retry exactly at the deadline is still allowed`() {
        val decision = POLICY.decide(SendOutcome.Transient("HTTP 503"), 0, CREATED, at(24 * 60).minusSeconds(10))
        assertEquals(Decision.Retry(at(24 * 60), attempts = 1, "HTTP 503"), decision)
    }

    @Test
    fun `a message is due until its deadline`() {
        assertEquals(false, POLICY.expired(CREATED, at(24 * 60)))
        assertEquals(true, POLICY.expired(CREATED, at(24 * 60).plusNanos(1000)))
    }

    @Test
    fun `a delivery found past its time to live expires with its attempts`() {
        assertEquals(Decision.Expired("not delivered within PT24H", attempts = 3), POLICY.expiry(3))
    }

    @Test
    fun `settings are validated`() {
        assertFailsWith<IllegalArgumentException> { RetrySettings(initialDelay = Duration.ZERO).validated() }
        assertFailsWith<IllegalArgumentException> { RetrySettings(maxDelay = Duration.ofSeconds(1)).validated() }
        assertFailsWith<IllegalArgumentException> { RetrySettings(maxAttempts = 0).validated() }
        assertFailsWith<IllegalArgumentException> { RetrySettings(maxRetryAfter = Duration.ZERO).validated() }
        assertFailsWith<IllegalArgumentException> { RetrySettings(ttl = Duration.ZERO).validated() }
        assertEquals(RetrySettings(), RetrySettings().validated())
    }
}
