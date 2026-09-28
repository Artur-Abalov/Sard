// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

/** The hostname rule Enroll applies and Register repeats: what the OS reported, 1 to 253 characters. */
object Hostnames {
    const val MAX_LENGTH = 253

    fun isValid(hostname: String): Boolean = hostname.isNotEmpty() && hostname.length <= MAX_LENGTH
}
