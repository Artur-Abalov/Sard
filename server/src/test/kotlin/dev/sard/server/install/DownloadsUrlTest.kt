// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

import dev.sard.server.enrollment.AgentEndpoint
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

private const val HTTP_PORT = 8080

/** Rule "Адрес сервера в командах — тот же, что в команде enroll" (В1): where the packages are fetched from. */
@MutFlowTest
class DownloadsUrlTest {
    private fun resolve(
        setting: String,
        endpoint: String = "backup.corp.example:443",
    ) = MutFlow.underTest { DownloadsUrl.resolve(setting, AgentEndpoint(endpoint), HTTP_PORT) }

    @Test
    fun `Адрес раздачи по умолчанию строится из хоста AgentEndpoint и порта HTTP`() {
        assertEquals(
            "http://backup.corp.example:8080/downloads/agent/sard-agent.deb",
            resolve("").file("sard-agent.deb"),
        )
    }

    @Test
    fun `Адрес IPv6 в скобках остаётся адресом`() {
        assertEquals("http://[::1]:8080/downloads/agent/x", resolve("", endpoint = "[::1]:9090").file("x"))
    }

    @Test
    fun `SARD_AGENT_DOWNLOADS_URL задаёт адрес раздачи`() {
        assertEquals(
            "https://sard.corp.example/downloads/agent/x",
            resolve("https://sard.corp.example").file("x"),
        )
    }

    @Test
    fun `Косая черта в конце и путь перед downloads сохраняются правильно`() {
        assertEquals("https://corp.example/sard/downloads/agent/x", resolve(" https://corp.example/sard/ ").file("x"))
    }

    @Test
    fun `Сервер не стартует с адресом раздачи не http и не https`() {
        for (bad in listOf("ftp://sard.corp.example", "sard.corp.example", "/downloads", "https://", "http:///x")) {
            val e = assertFailsWith<IllegalArgumentException>(bad) { resolve(bad) }
            assertContains(e.message!!, "SARD_AGENT_DOWNLOADS_URL")
        }
    }

    @Test
    fun `Адрес с запросом или фрагментом отклоняется`() {
        for (bad in listOf("https://sard.corp.example/?a=1", "https://sard.corp.example/#top")) {
            assertFailsWith<IllegalArgumentException>(bad) { resolve(bad) }
        }
    }

    @Test
    fun `Адрес с символами, которые ломают команду оболочки, отклоняется`() {
        for (bad in listOf("https://sard.corp.example/a'b", "https://sard.corp.example/a b", "https://x/\$(id)")) {
            assertFailsWith<IllegalArgumentException>(bad) { resolve(bad) }
        }
    }
}
