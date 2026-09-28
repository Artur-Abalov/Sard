// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

/**
 * The hostname rule Enroll applies and Register repeats (spec decision 8): what the OS
 * reported, 1 to 253 characters, no control character (U+0000–U+001F, U+007F–U+009F).
 * NUL in particular: PostgreSQL cannot store it, so it would fail the write as a retryable
 * internal error instead of the agent's own INVALID_ARGUMENT.
 */
object Hostnames {
    const val MAX_LENGTH = 253

    fun isValid(hostname: String): Boolean {
        val fits = hostname.isNotEmpty() && hostname.length <= MAX_LENGTH
        return fits && hostname.none(Char::isISOControl)
    }
}
