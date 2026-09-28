// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

private const val IPV4_OCTETS = 4
private const val IPV4_OCTET_MAX = 255
private const val IPV4_OCTET_MAX_DIGITS = 3

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

// "0" on its own, or 1-3 ascii digits not starting with '0' (Go's net.ParseIP — the agent is
// the verifier — rejects zero-padded octets, and this shape also rejects a sign such as "+1").
private val IPV4_OCTET_SHAPE = Regex("^(0|[1-9][0-9]{0,${IPV4_OCTET_MAX_DIGITS - 1}})$")

private fun parseOctet(text: String): Int? {
    if (!IPV4_OCTET_SHAPE.matches(text)) return null
    return text.toInt().takeIf { it <= IPV4_OCTET_MAX }
}
