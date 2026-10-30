// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.Subject
import java.security.SecureRandom
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

private val IDLE_TIMEOUT: Duration = Duration.ofHours(1)
private val ABSOLUTE_TIMEOUT: Duration = Duration.ofHours(8)

/** The reasons a session expires (the field reason of session.expired). */
const val IDLE = "idle"
const val ABSOLUTE = "absolute"
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
    /** The address of the request that last used the session (sign-in until the first one). */
    val clientAddress: String = "",
    /** Who the session stands for; the access policy is asked about it (CORE1 Р4). */
    val subject: Subject = Subject.ADMINISTRATOR,
)

/**
 * Sessions live only in this process's memory: a restart ends every one of them (Р8).
 * [touch] is both the read and the "activity" that extends the idle deadline (Р1):
 * every authenticated request under /api/v1 touches the session it carries.
 */
class SessionStore(
    private val clock: Clock,
    private val random: SecureRandom = SecureRandom(),
    /** Told once, with the reason ([IDLE] or [ABSOLUTE]), of each session that ran out; not of one that signed out. */
    private val onExpired: (AdminSession, String) -> Unit = { _, _ -> },
) {
    private val sessions = ConcurrentHashMap<String, AdminSession>()

    /** Also sweeps expired sessions: a cheap, bounded opportunity, since nothing else ever iterates the whole map. */
    fun create(
        tenantId: UUID,
        clientAddress: String = "",
    ): AdminSession {
        val now = clock.instant()
        sweepExpired(now)
        val session = AdminSession(newId(), tenantId, now, now, clientAddress)
        sessions[session.id] = session
        return session
    }

    /** The session if [id] names one that is still valid; touching extends its idle deadline to now. */
    fun touch(
        id: String,
        clientAddress: String? = null,
    ): AdminSession? {
        val now = clock.instant()
        val current = sessions[id]
        val valid = current?.takeIf { isValid(it, now) }
        if (valid == null) {
            current?.let { expire(it) }
            return null
        }
        val touched = valid.copy(lastActivityAt = now, clientAddress = clientAddress ?: valid.clientAddress)
        sessions[id] = touched
        return touched
    }

    /**
     * The session if [id] names one that is still valid, without touching it: for callers that
     * know a guard already touched the same session earlier in this request (Р1's extension
     * happens once, in [SessionAuthFilter], not again here).
     */
    fun find(id: String): AdminSession? {
        val now = clock.instant()
        return sessions[id]?.takeIf { isValid(it, now) }
    }

    /** True when [id] named a session that was removed. */
    fun remove(id: String): Boolean = sessions.remove(id) != null

    /** Ends every session: the password changed, so none of the others may go on (F4a, Р9). */
    fun removeAll() = sessions.clear()

    /** How many sessions are tracked right now, expired or not; for tests only. */
    internal fun trackedSessions(): Int = sessions.size

    /** min(last activity + 1h, login + 8h), as shown to the client (Р1). */
    fun expiresAt(session: AdminSession): Instant {
        val idleDeadline = session.lastActivityAt + IDLE_TIMEOUT
        val absoluteDeadline = session.createdAt + ABSOLUTE_TIMEOUT
        return minOf(idleDeadline, absoluteDeadline)
    }

    private fun isValid(
        session: AdminSession,
        now: Instant,
    ): Boolean = now.isBefore(expiresAt(session))

    /** Removes every session that ran out and tells [onExpired] of each; the periodic sweep (Р23) calls it. */
    fun sweep() = sweepExpired(clock.instant())

    private fun sweepExpired(now: Instant) {
        sessions.values.filterNot { isValid(it, now) }.forEach { expire(it) }
    }

    /** Removes [session], which ran out, and tells of it once, whoever of the callers gets to it first. */
    private fun expire(session: AdminSession) {
        if (!sessions.remove(session.id, session)) return
        val idleFirst = session.lastActivityAt + IDLE_TIMEOUT <= session.createdAt + ABSOLUTE_TIMEOUT
        runCatching { onExpired(session, if (idleFirst) IDLE else ABSOLUTE) }
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
