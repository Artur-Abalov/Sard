// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.console

import dev.sard.server.SeededAdministrator
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.downloads.AgentPackageFixture
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.test.context.DynamicPropertyRegistry
import org.springframework.test.context.DynamicPropertySource
import java.nio.file.Path
import java.util.Properties
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val SERVER_VERSION: String =
    Properties()
        .apply {
            ConsoleWithAgentDownloadsIntegrationTest::class.java
                .getResourceAsStream("/META-INF/build-info.properties")!!
                .use { load(it) }
        }.getProperty("build.version")

private val RELEASE: Path = AgentPackageFixture.tempRelease(SERVER_VERSION)

/**
 * The image serves both the console and the agent packages (U1a): `/downloads` belongs to the
 * packages, so the console's filter never answers there (docs/specs/server/console-serving.feature).
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [
        "spring.grpc.server.port=0",
        "sard.test.admin-password=$CONSOLE_PASSWORD",
        "sard.console.location=$FIXTURE_CONSOLE",
        "sard.agent-packages.enabled=true",
    ],
)
@Import(SeededAdministrator::class, TestcontainersConfiguration::class)
class ConsoleWithAgentDownloadsIntegrationTest(
    @LocalServerPort port: Int,
) {
    companion object {
        @JvmStatic
        @DynamicPropertySource
        fun packages(registry: DynamicPropertyRegistry) {
            registry.add("sard.agent-packages.dir") { RELEASE.toString() }
        }
    }

    private val http = ConsoleHttp(port)

    @Test
    fun `Пакеты агента отдаются рядом с консолью`() {
        val response = http.get("/downloads/agent/manifest.json")
        assertEquals(200, response.statusCode())
        assertTrue(response.body().contains("\"version\": \"$SERVER_VERSION\""), response.body())
        assertNull(response.headers().firstValue("Content-Security-Policy").orElse(null))
    }

    @Test
    fun `Путь пакетов агента без файла не отдаёт страницу консоли`() {
        listOf(
            "/downloads",
            "/downloads/",
            "/downloads/agent",
            "/downloads/agent/",
            "/downloads/agent/no-such",
        ).forEach {
            val response = http.get(it)
            assertFalse(response.body().contains(FIXTURE_PAGE_MARK), it)
            assertNull(response.headers().firstValue("Content-Security-Policy").orElse(null), it)
        }
    }
}
