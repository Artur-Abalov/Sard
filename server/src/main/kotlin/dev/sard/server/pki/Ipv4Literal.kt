// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

private const val IPV4_OCTETS = 4
private const val IPV4_OCTET_MAX = 255

/** Manual IPv4 literal parsing (no [java.net.InetAddress]): see [ServerNames] for why. */
internal object Ipv4Literal {
    /** The 4 address bytes if [text] is a dotted-decimal IPv4 literal, else null. An out-of-range
     * octet (e.g. "999.1.1.1") is rejected here, not resolved as a hostname. */
    fun parse(text: String): ByteArray? {
        val parts = text.split('.')
        if (parts.size != IPV4_OCTETS) return null
        val octets = parts.mapNotNull(::parseOctet)
        return if (octets.size == IPV4_OCTETS) ByteArray(IPV4_OCTETS) { octets[it].toByte() } else null
    }
}

private fun parseOctet(text: String): Int? = text.toIntOrNull()?.takeIf { it in 0..IPV4_OCTET_MAX }
