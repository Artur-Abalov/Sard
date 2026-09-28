// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

private const val GRPC_PORT = 9090
private val CERT_NAMES = listOf("sard.example.com", "localhost", "127.0.0.1")

/** Decision 5 of docs/specs/server/agent-enrollment.feature: the address in the enroll command. */
@MutFlowTest
class AgentEndpointTest {
    private fun resolve(
        configured: String?,
        names: List<String> = CERT_NAMES,
        port: Int = GRPC_PORT,
    ) = MutFlow.underTest { AgentEndpointResolver.resolve(configured, names, port) }

    @Test
    fun `Незаданный адрес для агентов даёт команду с первым именем сертификата и портом gRPC`() {
        assertEquals("sard.example.com:9090", resolve(null).address)
    }

    @Test
    fun `Заданный адрес с хостом из сертификата попадает в команду как есть`() {
        assertEquals("localhost:19090", resolve("localhost:19090").address)
    }

    @Test
    fun `Адрес с IP-адресом из сертификата принимается`() {
        assertEquals("127.0.0.1:9090", resolve("127.0.0.1:9090").address)
    }

    @Test
    fun `Первое имя сертификата в виде IPv6 попадает в команду в скобках`() {
        assertEquals("[::1]:9090", resolve(null, names = listOf("::1", "localhost")).address)
    }

    @Test
    fun `Адрес без порта получает порт gRPC сервера`() {
        assertEquals("sard.example.com:9090", resolve("sard.example.com").address)
    }

    @Test
    fun `Имя хоста сверяется с сертификатом без учёта регистра`() {
        assertEquals("SARD.Example.com:9090", resolve("SARD.Example.com:9090").address)
    }

    @Test
    fun `IPv6-адрес в скобках сверяется с сертификатом по значению`() {
        val names = listOf("sard.example.com", "::1")
        assertEquals("[0:0:0:0:0:0:0:1]:9090", resolve("[0:0:0:0:0:0:0:1]:9090", names = names).address)
    }

    @Test
    fun `Адрес с хостом вне сертификата не даёт серверу стартовать`() {
        val error = assertFailsWith<InvalidAgentEndpointException> { resolve("backup.example.org:9090") }
        assertTrue("backup.example.org" in error.message.orEmpty(), error.message.orEmpty())
        for (name in CERT_NAMES) assertTrue(name in error.message.orEmpty(), error.message.orEmpty())
    }

    @Test
    fun `Адрес с IP-адресом вне сертификата не даёт серверу стартовать`() {
        val error = assertFailsWith<InvalidAgentEndpointException> { resolve("10.0.0.7:9090") }
        assertTrue("10.0.0.7" in error.message.orEmpty(), error.message.orEmpty())
        for (name in CERT_NAMES) assertTrue(name in error.message.orEmpty(), error.message.orEmpty())
    }

    @Test
    fun `Неразбираемый адрес для агентов не даёт серверу стартовать`() {
        val values =
            listOf(
                "https://sard.example.com:9090",
                ":9090",
                "sard.example.com:0",
                "sard.example.com:65536",
                "sard.example.com:port",
                "::1",
                "[::1",
                "[]:9090",
                "[::1]extra",
                "[::1]:99999",
            )
        for (value in values) {
            val error = assertFailsWith<InvalidAgentEndpointException>(value) { resolve(value) }
            val message = error.message.orEmpty()
            assertTrue("SARD_AGENT_ENDPOINT" in message, "$value: $message")
            assertTrue(value in message, "$value: $message")
        }
    }

    @Test
    fun `server names must not be empty`() {
        assertFailsWith<IllegalArgumentException> { resolve(null, names = emptyList()) }
        assertFailsWith<IllegalArgumentException> { resolve("localhost:9090", names = emptyList()) }
    }

    @Test
    fun `a host shaped like an IPv4 address but out of range is not covered, not resolved by DNS`() {
        val error = assertFailsWith<InvalidAgentEndpointException> { resolve("999.999.999.999:9090") }
        assertTrue("999.999.999.999" in error.message.orEmpty(), error.message.orEmpty())
    }

    @Test
    fun `an IPv4-mapped IPv6 server name covers the same address written in hex`() {
        val names = listOf("::ffff:10.0.0.1")
        assertEquals("[::ffff:a00:1]:9090", resolve("[::ffff:a00:1]:9090", names = names).address)
    }

    @Test
    fun `the maximum IPv4 octet 255 covers its hex-hextet spelling, byte for byte`() {
        // Forces byte-exact IPv4 parsing of the maximum octet: a fallback to DNS-shaped string
        // comparison could never match these two different spellings of the same address.
        assertEquals(
            "[::ffff:255.255.255.255]:9090",
            resolve("[::ffff:255.255.255.255]:9090", names = listOf("::ffff:ffff:ffff")).address,
        )
    }

    @Test
    fun `256 does not fit an IPv4 octet and must not be silently accepted as if it wrapped to 0`() {
        val error =
            assertFailsWith<InvalidAgentEndpointException> {
                resolve("256.0.0.1:9090", names = listOf("0.0.0.1"))
            }
        assertTrue("256.0.0.1" in error.message.orEmpty(), error.message.orEmpty())
    }

    @Test
    fun `a leading double colon before an embedded IPv4 covers the same address spelled in hex hextets`() {
        // "::1.2.3.4" only parses correctly if the "::" survives the tail split (decision 5в);
        // matching it against a plainly-spelled hex form forces byte-exact, not string, comparison.
        assertEquals(
            "[::1.2.3.4]:9090",
            resolve("[::1.2.3.4]:9090", names = listOf("0:0:0:0:0:0:102:304")).address,
        )
    }

    @Test
    fun `six hextets plus an embedded IPv4, with no compression, cover the same address in hex hextets`() {
        // Exercises the exact group-count check (6 hextets + the embedded IPv4's 2 groups == 8)
        // against a differently-spelled but byte-identical name, so only a byte-correct parse matches.
        assertEquals(
            "[1:2:3:4:5:6:1.2.3.4]:9090",
            resolve("[1:2:3:4:5:6:1.2.3.4]:9090", names = listOf("1:2:3:4:5:6:102:304")).address,
        )
    }

    @Test
    fun `a mid-address compression covers the same address spelled in full, group for group`() {
        // "1:2::7:8" and "1:2:0:0:0:0:7:8" name the same address only if the compressed form fills
        // exactly the missing four groups with zero, at the right byte offsets.
        assertEquals(
            "[1:2::7:8]:9090",
            resolve("[1:2::7:8]:9090", names = listOf("1:2:0:0:0:0:7:8")).address,
        )
    }

    @Test
    fun `a compression filling exactly one omitted group covers the same address spelled in full`() {
        assertEquals(
            "[1:2:3:4:5:6:7::]:9090",
            resolve("[1:2:3:4:5:6:7::]:9090", names = listOf("1:2:3:4:5:6:7:0")).address,
        )
    }

    @Test
    fun `a compression that leaves no group to fill is malformed, not a redundant no-op`() {
        // "1:2:3:4:5:6:7::8" already spells out all 8 groups before the "::"; RFC 4291 requires
        // "::" to represent at least one omitted group, so this must not be silently accepted as
        // if it were plain "1:2:3:4:5:6:7:8" with zero groups filled in.
        val error =
            assertFailsWith<InvalidAgentEndpointException> {
                resolve("[1:2:3:4:5:6:7::8]:9090", names = listOf("1:2:3:4:5:6:7:8"))
            }
        assertTrue("1:2:3:4:5:6:7::8" in error.message.orEmpty(), error.message.orEmpty())
    }
}
