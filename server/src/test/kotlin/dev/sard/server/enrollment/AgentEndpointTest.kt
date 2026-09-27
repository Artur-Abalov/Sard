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
}
