// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

import dev.sard.server.downloads.AgentOffer
import dev.sard.server.downloads.AgentPackagesProperties
import dev.sard.server.enrollment.AgentEndpoint
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.boot.test.context.ConfigDataApplicationContextInitializer
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import org.springframework.context.annotation.Bean
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertNotNull

@TestConfiguration(proxyBeanMethods = false)
@EnableConfigurationProperties(AgentPackagesProperties::class)
private class Neighbours {
    @Bean
    fun offer(): AgentOffer = AgentOffer.Withheld("v1.4.0")

    @Bean
    fun endpoint() = AgentEndpoint("backup.corp.example:443", explicit = true)
}

/** Rule "Адрес сервера в командах" (В1): the setting is read, and a bad one stops the start. */
class InstallConfigurationTest {
    private val context =
        ApplicationContextRunner()
            .withInitializer(ConfigDataApplicationContextInitializer())
            .withUserConfiguration(Neighbours::class.java, InstallConfiguration::class.java)

    private fun fileUrl(vararg settings: String): String {
        var url = ""
        context.withPropertyValues(*settings).run {
            assertNotNull(it.getBean(AgentInstalls::class.java))
            url = it.getBean(DownloadsUrl::class.java).file("x")
        }
        return url
    }

    @Test
    fun `Без SARD_AGENT_DOWNLOADS_URL адрес раздачи - хост AgentEndpoint и порт HTTP`() {
        assertEquals("http://backup.corp.example:8080/downloads/agent/x", fileUrl())
        assertEquals("http://backup.corp.example:8081/downloads/agent/x", fileUrl("server.port=8081"))
    }

    @Test
    fun `SARD_AGENT_DOWNLOADS_URL задаёт адрес раздачи`() {
        assertEquals(
            "https://sard.corp.example/downloads/agent/x",
            fileUrl("sard.agent-packages.downloads-url=https://sard.corp.example"),
        )
    }

    @Test
    fun `Сервер не стартует с адресом раздачи не http и не https`() {
        var failure: Throwable? = null
        context.withPropertyValues("sard.agent-packages.downloads-url=ftp://sard.corp.example").run {
            failure = it.startupFailure
        }
        val messages = generateSequence(failure) { it.cause }.mapNotNull { it.message }.joinToString("\n")
        assertContains(messages, "SARD_AGENT_DOWNLOADS_URL")
    }

    @Test
    fun `Версия для сравнения - версия раздачи, а ключ релизов - ключ сборки`() {
        context.run {
            assertEquals(false, it.getBean(AgentVersions::class.java).outdated("v1.4.0"))
            assertEquals(true, it.getBean(AgentVersions::class.java).outdated("v1.3.9"))
            assertEquals(ReleaseKey.bundled(), it.getBean(ReleaseKey::class.java))
        }
    }
}
