// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.concurrent.ConcurrentHashMap

private val WINDOW: Duration = Duration.ofMinutes(15)
private const val MAX_FAILURES = 5

/**
 * Per-client-address brute-force lock (Р2): 5 failures in 15 minutes lock sign-in from
 * that address for 15 minutes from the failure that reached the threshold. A restart
 * (a fresh instance) clears every lock, since state lives only in memory.
 */
class LoginAttemptTracker(
    private val clock: Clock,
) {
    private class State {
        val failures = ArrayDeque<Instant>()
        var lockedAt: Instant? = null
    }

    private val byAddress = ConcurrentHashMap<String, State>()

    /**
     * Runs [block] (the whole lock check, password verification and outcome recording of
     * one sign-in attempt) while holding [address]'s own lock, so concurrent attempts
     * from the same address are serialized: the "at most 5 401 before a 429" guarantee
     * (rule "Одновременные неверные попытки не обходят порог") needs the check and the
     * failure it protects to be one atomic step, not two. [Clock]-based methods called
     * from [block] reenter the same lock safely (`synchronized` is per-thread reentrant).
     */
    fun <T> withAddressLock(
        address: String,
        block: () -> T,
    ): T {
        val state = byAddress.computeIfAbsent(address) { State() }
        val result = synchronized(state) { block() }
        forgetIfInert(address, state)
        return result
    }

    /** Bounds the map's size: an address with no failures in the window and no lock is dead weight. */
    private fun forgetIfInert(
        address: String,
        state: State,
    ) {
        synchronized(state) {
            pruneExpired(state, clock.instant())
            if (state.failures.isEmpty() && state.lockedAt == null) byAddress.remove(address, state)
        }
    }

    /** How many addresses are tracked right now, locked or not; for tests only. */
    internal fun trackedAddresses(): Int = byAddress.size

    /** Seconds until [address] may try again, rounded up; null when it is not locked. */
    fun retryAfterSeconds(address: String): Long? {
        val state = byAddress[address] ?: return null
        return synchronized(state) { retryAfterSecondsLocked(state) }
    }

    private fun retryAfterSecondsLocked(state: State): Long? {
        val lockedAt = state.lockedAt ?: return null
        val unlockAt = lockedAt + WINDOW
        val now = clock.instant()
        val stillLocked = now.isBefore(unlockAt)
        if (!stillLocked) {
            state.lockedAt = null
            state.failures.clear()
        }
        return if (stillLocked) ceilSeconds(Duration.between(now, unlockAt)) else null
    }

    /** Records a failure from [address]; returns true exactly when this failure just locked it. */
    fun recordFailure(address: String): Boolean {
        val state = byAddress.computeIfAbsent(address) { State() }
        synchronized(state) {
            val now = clock.instant()
            pruneExpired(state, now)
            state.failures.addLast(now)
            return locksNow(state, now)
        }
    }

    /** Drops every failure in [state] that fell out of the window as of [now]. */
    private fun pruneExpired(
        state: State,
        now: Instant,
    ) {
        while (state.failures.isNotEmpty() && state.failures.first() <= now - WINDOW) {
            state.failures.removeFirst()
        }
    }

    /** True exactly when [state] just reached the failure threshold and was not already locked. */
    private fun locksNow(
        state: State,
        now: Instant,
    ): Boolean {
        if (state.failures.size < MAX_FAILURES || state.lockedAt != null) return false
        state.lockedAt = now
        return true
    }

    /** A successful sign-in from [address] forgets its failure history. */
    fun recordSuccess(address: String) {
        byAddress.remove(address)
    }

    private fun ceilSeconds(remaining: Duration): Long {
        val whole = remaining.seconds
        val extra = if (remaining.nano > 0) 1 else 0
        return maxOf(whole + extra, 1)
    }
}
