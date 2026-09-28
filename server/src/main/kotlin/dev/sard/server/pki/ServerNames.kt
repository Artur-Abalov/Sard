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
    /** The address bytes (4 for IPv4, 16 for IPv6) if [name] is an IP-address literal, else null.
     * The single parser: [Certificates] builds the IP SAN straight from these bytes rather than
     * handing the string to BouncyCastle's own (looser) parser. */
    fun ipLiteral(name: String): ByteArray? = Ipv4Literal.parse(name) ?: Ipv6Literal.parse(name)

    /** True if [name] parses as an IPv4 or IPv6 address literal; false — including for a merely
     * IPv4-shaped but out-of-range name — means it is a DNS name. */
    fun isIpLiteral(name: String): Boolean = ipLiteral(name) != null

    /** [host] is covered by [names] if one of them is the same address (IP literals compared by
     * value, not spelling) or, for DNS names, the same name case-insensitively (decision 5г). */
    fun covers(
        host: String,
        names: List<String>,
    ): Boolean {
        val hostIp = ipLiteral(host)
        return if (hostIp != null) {
            names.any { name -> ipLiteral(name)?.contentEquals(hostIp) == true }
        } else {
            names.any { name -> ipLiteral(name) == null && name.equals(host, ignoreCase = true) }
        }
    }

    /** Fails if any of [names] is neither an IP literal nor an RFC 1123 hostname whose last label
     * is not all digits — so a name that would otherwise be silently issued as a dNSName SAN (and
     * never match anything) stops the CA at startup instead. */
    fun validate(names: List<String>) {
        for (name in names) {
            require(ipLiteral(name) != null || isHostname(name)) {
                "sard.pki.server-names entry is neither an IP literal nor a hostname: '$name'"
            }
        }
    }

    /** A dot-separated hostname of 1-255 characters where every label matches
     * [HOSTNAME_LABEL] and the last is not all digits (that shape belongs to an IP literal, not
     * a hostname, and "999.1.1.1" must not be silently accepted as one). */
    private val HOSTNAME = Regex("^(?=.{1,$MAX_HOSTNAME_LENGTH}$)(?:$HOSTNAME_LABEL\\.)*$LAST_LABEL$")

    private fun isHostname(name: String): Boolean = HOSTNAME.matches(name)
}

private const val MAX_HOSTNAME_LENGTH = 255
private const val HOSTNAME_LABEL = "[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?"

/** A [HOSTNAME_LABEL] that also contains at least one non-digit, so a bare-number last label
 * ("999.1.1.1"'s "1") never counts as a hostname's final label. */
private const val LAST_LABEL = "(?=[A-Za-z0-9-]*[A-Za-z-])$HOSTNAME_LABEL"
