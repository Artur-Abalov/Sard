// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import java.security.SecureRandom
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

private val IDLE_TIMEOUT: Duration = Duration.ofHours(12)
private val ABSOLUTE_TIMEOUT: Duration = Duration.ofDays(7)
private const val ID_BYTES = 32
private val HEX = "0123456789abcdef".toCharArray()
private const val BYTE_MASK = 0xFF
private const val LOW_NIBBLE_MASK = 0x0F
private const val NIBBLE_BITS = 4
private const val HEX_CHARS_PER_BYTE = 2

/** An in-memory server-side administrator session (D2, Р7): no cap on how many, no persistence. */
data class AdminSession(
    val id: String,
    val tenantId: UUID,
    val createdAt: Instant,
    val lastActivityAt: Instant,
)

/**
 * Sessions live only in this process's memory: a restart ends every one of them (Р8).
 * [touch] is both the read and the "activity" that extends the idle deadline (Р1):
 * every authenticated request under /api/v1 touches the session it carries.
 */
class SessionStore(
    private val clock: Clock,
    private val random: SecureRandom = SecureRandom(),
) {
    private val sessions = ConcurrentHashMap<String, AdminSession>()

    /** Also sweeps expired sessions: a cheap, bounded opportunity, since nothing else ever iterates the whole map. */
    fun create(tenantId: UUID): AdminSession {
        val now = clock.instant()
        sweepExpired(now)
        val session = AdminSession(newId(), tenantId, now, now)
        sessions[session.id] = session
        return session
    }

    /** The session if [id] names one that is still valid; touching extends its idle deadline to now. */
    fun touch(id: String): AdminSession? {
        val now = clock.instant()
        val valid = sessions[id]?.takeIf { isValid(it, now) }
        if (valid == null) {
            sessions.remove(id)
            return null
        }
        val touched = valid.copy(lastActivityAt = now)
        sessions[id] = touched
        return touched
    }

    /** True when [id] named a session that was removed. */
    fun remove(id: String): Boolean = sessions.remove(id) != null

    /** How many sessions are tracked right now, expired or not; for tests only. */
    internal fun trackedSessions(): Int = sessions.size

    /** min(last activity + 12h, login + 7d), as shown to the client (Р1). */
    fun expiresAt(session: AdminSession): Instant {
        val idleDeadline = session.lastActivityAt + IDLE_TIMEOUT
        val absoluteDeadline = session.createdAt + ABSOLUTE_TIMEOUT
        return minOf(idleDeadline, absoluteDeadline)
    }

    private fun isValid(
        session: AdminSession,
        now: Instant,
    ): Boolean = now.isBefore(expiresAt(session))

    private fun sweepExpired(now: Instant) {
        sessions.values.removeIf { !isValid(it, now) }
    }

    /** 32 random bytes (256 bits, well over the 128-bit floor) as lowercase hex. */
    private fun newId(): String {
        val bytes = ByteArray(ID_BYTES)
        random.nextBytes(bytes)
        val chars = CharArray(bytes.size * HEX_CHARS_PER_BYTE)
        for (i in bytes.indices) {
            val b = bytes[i].toInt() and BYTE_MASK
            chars[i * HEX_CHARS_PER_BYTE] = HEX[b ushr NIBBLE_BITS]
            chars[i * HEX_CHARS_PER_BYTE + 1] = HEX[b and LOW_NIBBLE_MASK]
        }
        return String(chars)
    }
}
