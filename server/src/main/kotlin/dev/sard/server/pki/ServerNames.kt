// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

private const val IPV6_GROUPS = 8
private const val IPV4_OCTETS = 4
private const val IPV4_OCTET_MAX = 255

// A hextet is 1-4 hex digits; 4 hex digits is exactly 0xFFFF, so a length check alone already
// bounds the parsed value — no separate range check is needed.
private const val IPV6_HEX_DIGITS_MAX = 4
private const val HEX_RADIX = 16
private const val BYTE_MASK = 0xFF
private const val BYTE_SHIFT = 8
private const val IPV6_BYTES = 16
private const val IPV4_MAPPED_OFFSET = 12

/**
 * Classifies server certificate SAN names as an IP literal or a DNS name, and matches a host
 * against a list of such names by value rather than spelling. The single place both the
 * certificate profile ([Certificates.generalName]) and the agent endpoint's host matching
 * (`AgentEndpointResolver.covered`) use, so the two agree (docs/specs/server/agent-enrollment.feature
 * decision 5в, 5г).
 *
 * Parsing is entirely manual (no [java.net.InetAddress]): an address is classified as an IP
 * literal only if it parses as one, with no resolver/DNS lookup ever performed — including for a
 * name that merely looks IPv4-shaped (four dot-separated groups) but has an out-of-range octet,
 * such as "999.1.1.1", which is therefore not an IP literal.
 */
internal object ServerNames {
    /** True if [name] parses as an IPv4 or IPv6 address literal; false — including for a merely
     * IPv4-shaped but out-of-range name — means it is a DNS name. */
    fun isIpLiteral(name: String): Boolean = parseIpLiteral(name) != null

    /** [host] is covered by [names] if one of them is the same address (IP literals compared by
     * value, not spelling) or, for DNS names, the same name case-insensitively (decision 5г). */
    fun covers(
        host: String,
        names: List<String>,
    ): Boolean {
        val hostIp = parseIpLiteral(host)
        return if (hostIp != null) {
            names.any { name -> parseIpLiteral(name)?.contentEquals(hostIp) == true }
        } else {
            names.any { name -> parseIpLiteral(name) == null && name.equals(host, ignoreCase = true) }
        }
    }
}

/** The address bytes (4 for IPv4, 16 for IPv6) if [text] is an IP-address literal, else null. */
private fun parseIpLiteral(text: String): ByteArray? = parseIPv4(text) ?: parseIPv6(text)

private fun parseIPv4(text: String): ByteArray? {
    val parts = text.split('.')
    if (parts.size != IPV4_OCTETS) return null
    return parts.mapOrNull(::parseOctet)?.let { octets -> ByteArray(IPV4_OCTETS) { octets[it].toByte() } }
}

private fun parseOctet(text: String): Int? = text.toIntOrNull()?.takeIf { it in 0..IPV4_OCTET_MAX }

/** RFC 4291: 8 groups of hex digits, "::" compressing one run of zero groups, an optional
 * trailing embedded IPv4 in place of the last two groups. */
private fun parseIPv6(text: String): ByteArray? {
    if (':' !in text) return null
    return splitEmbeddedIPv4(text)?.let { parts ->
        val ipv4Groups = if (parts.embeddedIPv4 != null) 2 else 0
        expandGroups(parts.head, ipv4Groups)?.let { groups -> ipv6Bytes(groups, parts.embeddedIPv4) }
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
        parseIPv4(tail)?.let { HeadAndEmbeddedIPv4(text.substring(0, lastColon), it) }
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
    return both(parseHextets(sides[0]), parseHextets(sides[1])) { left, right ->
        val missing = IPV6_GROUPS - left.size - right.size - ipv4Groups
        if (missing < 0) null else left + List(missing) { 0 } + right
    }
}

/** The hextets of [text] (empty means none, as on either side of "::"), or null if malformed. */
private fun parseHextets(text: String): List<Int>? {
    if (text.isEmpty()) return emptyList()
    return text.split(':').mapOrNull(::parseHextet)
}

private fun parseHextet(text: String): Int? {
    if (text.isEmpty() || text.length > IPV6_HEX_DIGITS_MAX) return null
    return text.toIntOrNull(HEX_RADIX)
}
