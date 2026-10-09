// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import java.security.SecureRandom
import java.time.Clock
import java.time.Instant
import java.util.HexFormat
import java.util.concurrent.ConcurrentHashMap

private const val ID_BYTES = 32

/**
 * Setup sessions (Р3): in memory only, so a restart ends all of them; each lives as long as the code it was
 * exchanged for. An id the client sent before entering the code is never one of these.
 */
class SetupSessions(
    private val clock: Clock,
    private val random: SecureRandom = SecureRandom(),
) {
    private val expiries = ConcurrentHashMap<String, Instant>()

    /** A new session that ends at [expiresAt]; also drops the ones whose time has passed. */
    fun create(expiresAt: Instant): String {
        val now = clock.instant()
        expiries.values.removeIf { !now.isBefore(it) }
        val id = HexFormat.of().formatHex(ByteArray(ID_BYTES).also(random::nextBytes))
        expiries[id] = expiresAt
        return id
    }

    fun valid(id: String?): Boolean {
        val expiresAt = id?.let { expiries[it] } ?: return false
        return clock.instant().isBefore(expiresAt)
    }

    fun end(id: String) {
        expiries.remove(id)
    }

    fun endAll() = expiries.clear()

    /** How many sessions are held right now, expired or not; for tests only. */
    internal fun tracked(): Int = expiries.size
}
