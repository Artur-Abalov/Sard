// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.prometheus

import java.security.MessageDigest

private const val SCHEME = "Bearer "

/**
 * The token of the metrics listener (SARD_METRICS_TOKEN). It is compared as bytes with [MessageDigest.isEqual], which
 * takes the same time wherever the two differ, and it has no text form: neither [toString] nor a log line can print it.
 */
class BearerToken(
    token: String,
) {
    private val expected = (SCHEME + token).toByteArray(Charsets.UTF_8)

    /** Whether [authorization], the header of a request, is `Bearer <token>`. */
    fun accepts(authorization: String?): Boolean {
        val offered = (authorization ?: return false).toByteArray(Charsets.UTF_8)
        return MessageDigest.isEqual(expected, offered)
    }

    override fun toString() = "BearerToken(***)"
}
