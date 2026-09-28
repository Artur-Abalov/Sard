// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * The single classification and coverage rule [Certificates.generalName] (certificate SAN type)
 * and `AgentEndpointResolver.covered` (agent endpoint host matching) both use (F2, decision 5в).
 */
class ServerNamesTest {
    @Test
    fun `classification and coverage agree on IP literals and DNS names`() {
        val ipLiterals = listOf("10.0.0.1", "::1", "::ffff:10.0.0.1")
        val dnsNames = listOf("sard.example.com")
        for (name in ipLiterals) {
            assertTrue(ServerNames.isIpLiteral(name), "$name must classify as an IP literal")
            assertTrue(ServerNames.covers(name, listOf(name)), "$name must cover itself by the same parse")
        }
        for (name in dnsNames) {
            assertFalse(ServerNames.isIpLiteral(name), "$name must classify as a DNS name")
            assertTrue(ServerNames.covers(name, listOf(name)), "$name must cover itself by the same parse")
        }
    }

    @Test
    fun `an out-of-range IPv4-shaped name is classified without a resolver lookup`() {
        // 999 is not a valid octet; parsing is entirely manual (no java.net.InetAddress involved),
        // so this can never trigger a DNS lookup — it is a DNS-shaped name, not an IP literal.
        assertFalse(ServerNames.isIpLiteral("999.1.1.1"))
    }

    @Test
    fun `an IPv4-mapped IPv6 literal covers the same address written with an embedded dotted IPv4`() {
        assertTrue(ServerNames.covers("::ffff:a00:1", listOf("::ffff:10.0.0.1")))
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
        for (name in malformed) assertFalse(ServerNames.isIpLiteral(name), name)
    }

    @Test
    fun `a full, uncompressed IPv6 literal with all eight groups is an IP literal`() {
        assertTrue(ServerNames.isIpLiteral("1:2:3:4:5:6:7:8"))
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
        for (name in notLiterals) assertFalse(ServerNames.isIpLiteral(name), name)
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
        for (name in malformed) assertFalse(ServerNames.isIpLiteral(name), name)
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
        for (name in goAccepts) assertTrue(ServerNames.isIpLiteral(name), name)
        for (name in goRejects) assertFalse(ServerNames.isIpLiteral(name), name)
        assertContentEquals(ByteArray(12) + byteArrayOf(1, 2, 3, 4), ServerNames.ipLiteral("::1.2.3.4"))
    }
}
