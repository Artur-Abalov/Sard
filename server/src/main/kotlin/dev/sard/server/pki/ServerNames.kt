// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

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
private fun parseIpLiteral(text: String): ByteArray? = Ipv4Literal.parse(text) ?: Ipv6Literal.parse(text)
