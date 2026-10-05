// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.downloads

import org.slf4j.LoggerFactory
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.boot.info.BuildProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.core.Ordered
import org.springframework.web.servlet.HandlerMapping
import org.springframework.web.servlet.handler.SimpleUrlHandlerMapping
import tools.jackson.databind.ObjectMapper
import java.nio.file.Path

/**
 * `sard.agent-packages.enabled` (SARD_AGENT_DOWNLOADS, default true) and `.dir`
 * (SARD_AGENT_PACKAGES_DIR, default /usr/share/sard/agent-packages, where the image puts them).
 */
@ConfigurationProperties("sard.agent-packages")
data class AgentPackagesProperties(
    val enabled: Boolean = true,
    val dir: Path = Path.of("/usr/share/sard/agent-packages"),
)

/**
 * `/downloads/agent/<file>`: the agent packages of this server's version, without a session —
 * hosts download them before they have anything (docs/adr/00XX-draft-agent-release.md). The path
 * is outside `/api/v1`, so the admin session and origin filters do not apply. Switched on, the
 * server starts only with a valid release of its own version; switched off, the path is 404.
 */
@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(AgentPackagesProperties::class)
class AgentDownloadsConfiguration {
    @Bean
    fun agentDownloadsMapping(
        properties: AgentPackagesProperties,
        build: BuildProperties,
        mapper: ObjectMapper,
    ): HandlerMapping = SimpleUrlHandlerMapping(handlers(properties, build.version ?: "unknown", mapper), ORDER)

    private fun handlers(
        properties: AgentPackagesProperties,
        version: String,
        mapper: ObjectMapper,
    ): Map<String, Any> {
        if (!properties.enabled) {
            log.info("Agent downloads are off (SARD_AGENT_DOWNLOADS=false)")
            return emptyMap()
        }
        val catalog = AgentPackageDirectory.load(properties.dir, version, mapper)
        log.info(
            "Agent downloads: sard-agent {} at {}, {} files from {}",
            catalog.version,
            PATH,
            catalog.files.size,
            properties.dir,
        )
        return mapOf(PATH to AgentPackagesHandler(properties.dir, catalog))
    }

    private companion object {
        const val PATH = "/downloads/agent/**"

        // Ahead of the default static resource mapping (Ordered.LOWEST_PRECEDENCE - 1).
        const val ORDER = Ordered.LOWEST_PRECEDENCE - 2
        val log = LoggerFactory.getLogger(AgentDownloadsConfiguration::class.java)
    }
}
