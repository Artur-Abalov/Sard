// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import java.time.Duration
import java.time.Instant

private const val INITIAL_DELAY_SECONDS = 10L
private const val MAX_DELAY_MINUTES = 10L
private const val MAX_ATTEMPTS = 8
private const val MAX_RETRY_AFTER_HOURS = 1L
private const val TTL_HOURS = 24L

/** `sard.notify.*` numbers of the retry policy (S9a, answer В8). */
data class RetrySettings(
    val initialDelay: Duration = Duration.ofSeconds(INITIAL_DELAY_SECONDS),
    val maxDelay: Duration = Duration.ofMinutes(MAX_DELAY_MINUTES),
    val maxAttempts: Int = MAX_ATTEMPTS,
    val maxRetryAfter: Duration = Duration.ofHours(MAX_RETRY_AFTER_HOURS),
    /** A delivery not made within this long after its run is planned expires. */
    val ttl: Duration = Duration.ofHours(TTL_HOURS),
) {
    fun validated(): RetrySettings {
        requireDelays()
        require(maxAttempts >= 1) { "sard.notify.max-attempts must be at least 1" }
        require(ttl.isPositive) { "sard.notify.ttl must be positive" }
        return this
    }

    private fun requireDelays() {
        require(initialDelay.isPositive) { "sard.notify.initial-delay must be positive" }
        require(maxDelay >= initialDelay) { "sard.notify.max-delay must be at least sard.notify.initial-delay" }
        require(maxRetryAfter.isPositive) { "sard.notify.max-retry-after must be positive" }
    }
}

/** What happens to a delivery after a send attempt. */
sealed interface Decision {
    data object Delivered : Decision

    /** The formatter had nothing to say about the run (S9b rules). */
    data object Skipped : Decision

    /** Send again at [at]; [attempts] is the new count of attempts that count. */
    data class Retry(
        val at: Instant,
        val attempts: Int,
        val reason: String,
    ) : Decision

    data class Failed(
        val reason: String,
        val attempts: Int,
    ) : Decision

    data class Expired(
        val reason: String,
        val attempts: Int,
    ) : Decision
}

/**
 * 429 waits retry_after (capped) without counting an attempt; 5xx, network and timeouts back
 * off from [RetrySettings.initialDelay], doubling up to [RetrySettings.maxDelay], for at most
 * [RetrySettings.maxAttempts]; any other rejection fails at once. Nothing is retried past
 * [RetrySettings.ttl] from the delivery's creation.
 */
class RetryPolicy(
    private val settings: RetrySettings,
) {
    fun expired(
        createdAt: Instant,
        now: Instant,
    ): Boolean = now.isAfter(createdAt + settings.ttl)

    /** Before a send: a delivery past its time to live is not attempted. */
    fun expiry(attempts: Int): Decision.Expired = Decision.Expired("not delivered within ${settings.ttl}", attempts)

    fun decide(
        outcome: SendOutcome,
        attempts: Int,
        createdAt: Instant,
        now: Instant,
    ): Decision =
        when (outcome) {
            SendOutcome.Delivered -> {
                Decision.Delivered
            }

            is SendOutcome.Rejected -> {
                Decision.Failed(outcome.reason, attempts)
            }

            is SendOutcome.RetryAfter -> {
                val wait = minOf(outcome.wait, settings.maxRetryAfter)
                val reason = "HTTP 429: retry after ${outcome.wait.seconds} s"
                within(createdAt, Decision.Retry(now + wait, attempts, reason))
            }

            is SendOutcome.Transient -> {
                transient(outcome.reason, attempts + 1, createdAt, now)
            }
        }

    private fun transient(
        reason: String,
        attempts: Int,
        createdAt: Instant,
        now: Instant,
    ): Decision {
        if (attempts >= settings.maxAttempts) {
            return Decision.Failed("gave up after $attempts attempts: $reason", attempts)
        }
        return within(createdAt, Decision.Retry(now + backoff(attempts), attempts, reason))
    }

    /** 1 → the initial delay, then doubling; the shift stops before it could overflow. */
    private fun backoff(attempts: Int): Duration {
        val doubled = settings.initialDelay.multipliedBy(1L shl minOf(attempts - 1, MAX_SHIFT))
        return minOf(doubled, settings.maxDelay)
    }

    private fun within(
        createdAt: Instant,
        retry: Decision.Retry,
    ): Decision =
        if (expired(createdAt, retry.at)) {
            Decision.Expired("not delivered within ${settings.ttl}: ${retry.reason}", retry.attempts)
        } else {
            retry
        }

    private companion object {
        const val MAX_SHIFT = 30
    }
}
