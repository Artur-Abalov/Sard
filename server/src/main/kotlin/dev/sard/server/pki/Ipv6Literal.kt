// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

private const val IPV6_GROUPS = 8
private const val IPV6_BYTES = 16
private const val IPV4_MAPPED_OFFSET = 12

// A hextet is 1-4 hex digits; 4 hex digits is exactly 0xFFFF, so a length check alone already
// bounds the parsed value — no separate range check is needed.
private const val IPV6_HEX_DIGITS_MAX = 4
private const val HEX_RADIX = 16
private const val BYTE_MASK = 0xFF
private const val BYTE_SHIFT = 8

/**
 * Manual IPv6 literal parsing (no [java.net.InetAddress]): see [ServerNames] for why.
 *
 * RFC 4291: 8 groups of hex digits, "::" compressing one run of zero groups, an optional
 * trailing embedded IPv4 in place of the last two groups.
 */
internal object Ipv6Literal {
    /** The 16 address bytes if [text] is an IPv6 literal, else null. */
    fun parse(text: String): ByteArray? {
        if (':' !in text) return null
        return splitEmbeddedIPv4(text)?.let { parts ->
            val ipv4Groups = if (parts.embeddedIPv4 != null) 2 else 0
            expandGroups(parts.head, ipv4Groups)?.let { groups -> ipv6Bytes(groups, parts.embeddedIPv4) }
        }
    }
}

/** [text] with any trailing embedded dotted-IPv4 (RFC 4291 "::ffff:10.0.0.1" form) split off. */
private data class HeadAndEmbeddedIPv4(
    val head: String,
    val embeddedIPv4: ByteArray?,
)

private fun splitEmbeddedIPv4(text: String): HeadAndEmbeddedIPv4? {
    val lastColon = text.lastIndexOf(':')
    val tail = text.substring(lastColon + 1)
    return if ('.' !in tail) {
        HeadAndEmbeddedIPv4(text, null)
    } else {
        Ipv4Literal.parse(tail)?.let { HeadAndEmbeddedIPv4(text.substring(0, lastColon), it) }
    }
}

private fun ipv6Bytes(
    groups: List<Int>,
    embeddedIPv4: ByteArray?,
): ByteArray {
    val bytes = ByteArray(IPV6_BYTES)
    for (i in groups.indices) {
        bytes[i * 2] = (groups[i] shr BYTE_SHIFT).toByte()
        bytes[i * 2 + 1] = (groups[i] and BYTE_MASK).toByte()
    }
    embeddedIPv4?.copyInto(bytes, IPV4_MAPPED_OFFSET)
    return bytes
}

/** The 16-bit groups of the (possibly "::"-compressed) part of an IPv6 literal before any
 * embedded IPv4 tail, or null if [head] isn't shaped like one. */
private fun expandGroups(
    head: String,
    ipv4Groups: Int,
): List<Int>? {
    val compressedAt = head.indexOf("::")
    val doublyCompressed = compressedAt >= 0 && head.indexOf("::", compressedAt + 1) >= 0
    if (doublyCompressed) return null
    return if (compressedAt < 0) uncompressedGroups(head, ipv4Groups) else compressedGroups(head, ipv4Groups)
}

private fun uncompressedGroups(
    head: String,
    ipv4Groups: Int,
): List<Int>? = parseHextets(head)?.takeIf { it.size + ipv4Groups == IPV6_GROUPS }

private fun compressedGroups(
    head: String,
    ipv4Groups: Int,
): List<Int>? {
    val sides = head.split("::", limit = 2)
    return parseHextets(sides[0])?.let { left ->
        parseHextets(sides[1])?.let { right ->
            val missing = IPV6_GROUPS - left.size - right.size - ipv4Groups
            if (missing < 0) null else left + List(missing) { 0 } + right
        }
    }
}

/** The hextets of [text] (empty means none, as on either side of "::"), or null if malformed. */
private fun parseHextets(text: String): List<Int>? {
    if (text.isEmpty()) return emptyList()
    val parts = text.split(':')
    val hextets = parts.mapNotNull(::parseHextet)
    return if (hextets.size == parts.size) hextets else null
}

private fun parseHextet(text: String): Int? {
    if (text.isEmpty() || text.length > IPV6_HEX_DIGITS_MAX) return null
    return text.toIntOrNull(HEX_RADIX)
}
