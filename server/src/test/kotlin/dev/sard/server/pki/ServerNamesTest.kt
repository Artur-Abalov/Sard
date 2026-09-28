// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * The single classification and coverage rule [Certificates.generalName] (certificate SAN type)
 * and `AgentEndpointResolver.covered` (agent endpoint host matching) both use (F2, decision 5в).
 */
@MutFlowTest
class ServerNamesTest {
    private fun isIpLiteral(name: String) = MutFlow.underTest { ServerNames.isIpLiteral(name) }

    private fun ipLiteral(name: String) = MutFlow.underTest { ServerNames.ipLiteral(name) }

    private fun covers(
        host: String,
        names: List<String>,
    ) = MutFlow.underTest { ServerNames.covers(host, names) }

    private fun validate(names: List<String>) = MutFlow.underTest { ServerNames.validate(names) }

    @Test
    fun `validate accepts a name for being a hostname alone, and separately for being an IP literal alone`() {
        // Each name is accepted for a different one of the two either-or reasons; neither
        // condition is required of every name in the list.
        validate(listOf("sard.example.com"))
        validate(listOf("10.0.0.1"))
        for (name in listOf("999.1.1.1", "fe80::1%eth0", "bad name")) {
            assertFailsWith<IllegalArgumentException>(name) { validate(listOf(name)) }
        }
    }

    @Test
    fun `classification and coverage agree on IP literals and DNS names`() {
        val ipLiterals = listOf("10.0.0.1", "::1", "::ffff:10.0.0.1")
        val dnsNames = listOf("sard.example.com")
        for (name in ipLiterals) {
            assertTrue(isIpLiteral(name), "$name must classify as an IP literal")
            assertTrue(covers(name, listOf(name)), "$name must cover itself by the same parse")
        }
        for (name in dnsNames) {
            assertFalse(isIpLiteral(name), "$name must classify as a DNS name")
            assertTrue(covers(name, listOf(name)), "$name must cover itself by the same parse")
        }
    }

    @Test
    fun `an out-of-range IPv4-shaped name is classified without a resolver lookup`() {
        // 999 is not a valid octet; parsing is entirely manual (no java.net.InetAddress involved),
        // so this can never trigger a DNS lookup — it is a DNS-shaped name, not an IP literal.
        assertFalse(isIpLiteral("999.1.1.1"))
    }

    @Test
    fun `an IPv4-mapped IPv6 literal covers the same address written with an embedded dotted IPv4`() {
        assertTrue(covers("::ffff:a00:1", listOf("::ffff:10.0.0.1")))
    }

    @Test
    fun `a host is covered only by a matching name, never by the mere presence of an unrelated one`() {
        // Every name here is a DNS name distinct from the host: a DNS name is never covered just
        // because some other name in the list also happens to be a DNS name.
        assertFalse(covers("unmatched.example.com", listOf("sard.example.com", "other.example.com")))
        // Same for an IP-literal host: an unrelated IP in the list must not cover it either.
        assertFalse(covers("10.0.0.9", listOf("10.0.0.1", "sard.example.com")))
    }

    @Test
    fun `malformed IPv4-shaped names are never IP literals`() {
        val malformed =
            listOf(
                "1.2.3", // too few octets
                "1.2.3.4.5", // too many octets
                "1.2.3.a", // not numeric
                "1.2.3.-4", // negative
            )
        for (name in malformed) assertFalse(isIpLiteral(name), name)
    }

    @Test
    fun `every octet up to 255 is in range, 256 and above is not`() {
        assertTrue(isIpLiteral("255.255.255.255"), "255 is the maximum valid octet")
        assertContentEquals(byteArrayOf(-1, -1, -1, -1), ipLiteral("255.255.255.255"))
        assertFalse(isIpLiteral("256.0.0.1"), "256 no longer fits a single octet")
    }

    @Test
    fun `a full, uncompressed IPv6 literal with all eight groups is an IP literal with the groups in order`() {
        assertTrue(isIpLiteral("1:2:3:4:5:6:7:8"))
        val expected =
            byteArrayOf(0, 1, 0, 2, 0, 3, 0, 4, 0, 5, 0, 6, 0, 7, 0, 8)
        assertContentEquals(expected, ipLiteral("1:2:3:4:5:6:7:8"))
    }

    @Test
    fun `a compression in the middle fills exactly the missing groups with zero, on both sides of it`() {
        assertTrue(isIpLiteral("1:2::7:8"))
        val expected =
            byteArrayOf(0, 1, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 7, 0, 8)
        assertContentEquals(expected, ipLiteral("1:2::7:8"))
    }

    @Test
    fun `signed or zero-padded numbers are never IP literals`() {
        val notLiterals =
            listOf(
                "+1.2.3.4", // leading '+' sign on an octet
                "1.2.3.+4", // leading '+' sign on an octet
                "010.0.0.1", // zero-padded octet
                "::+f", // leading '+' sign on a hextet
                "::-1", // leading '-' sign on a hextet
                "+f::1", // leading '+' sign on a hextet
            )
        for (name in notLiterals) assertFalse(isIpLiteral(name), name)
    }

    @Test
    fun `malformed IPv6-shaped names are never IP literals`() {
        val malformed =
            listOf(
                "1:2:3:4:5:6:7:8:9", // nine groups, no compression to explain it
                "1:2:3:4:5:6:7:8:9::", // nine groups before "::" leaves no room to compress
                "::1::2", // two compressions
                "12345::", // a hextet longer than four digits
                "gggg::1", // not hex digits
                "1:2:3", // too few groups, no compression
            )
        for (name in malformed) assertFalse(isIpLiteral(name), name)
    }

    /** Expectations produced by Go 1.27 net.ParseIP — the agent is the verifier. */
    @Test
    fun `the IP literal grammar is Go net ParseIP's`() {
        val goAccepts =
            listOf(
                "::1.2.3.4",
                "1::1.2.3.4",
                "::0:1.2.3.4",
                "::ffff:1.2.3.4",
                "1:2:3:4:5:6:1.2.3.4",
                "1:2:3:4:5:6:7::",
                "::",
                "::1",
            )
        val goRejects =
            listOf(
                "1:2:3:4::5:6:7:8",
                "::1:2:3:4:5:6:7:8",
                "1:2:3:4:5:6:7:8::",
                "1:2:3:4:5:6::1.2.3.4",
                "00000::1",
                ":1:2:3:4:5:6:7",
                "1:2:3:4:5:6:7:",
                "::ffff:010.0.0.1",
            )
        for (name in goAccepts) assertTrue(isIpLiteral(name), name)
        for (name in goRejects) assertFalse(isIpLiteral(name), name)
        assertContentEquals(ByteArray(12) + byteArrayOf(1, 2, 3, 4), ipLiteral("::1.2.3.4"))
    }
}
