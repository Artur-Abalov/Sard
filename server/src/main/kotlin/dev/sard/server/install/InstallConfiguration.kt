// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

import dev.sard.server.downloads.AgentOffer
import dev.sard.server.downloads.AgentPackagesProperties
import dev.sard.server.enrollment.AgentEndpoint
import org.springframework.beans.factory.annotation.Value
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration

/**
 * The beans of the install block (U1b). A bad SARD_AGENT_DOWNLOADS_URL, or a build without the release
 * key, stops the server here instead of giving administrators commands that cannot work.
 */
@Configuration(proxyBeanMethods = false)
class InstallConfiguration {
    @Bean
    fun downloadsUrl(
        properties: AgentPackagesProperties,
        endpoint: AgentEndpoint,
        @Value("\${server.port}") httpPort: Int,
    ): DownloadsUrl = DownloadsUrl.resolve(properties.downloadsUrl, endpoint, httpPort)

    @Bean
    fun releaseKey(): ReleaseKey = ReleaseKey.bundled()

    @Bean
    fun agentInstalls(
        offer: AgentOffer,
        downloads: DownloadsUrl,
        endpoint: AgentEndpoint,
        key: ReleaseKey,
    ): AgentInstalls = AgentInstalls(offer, InstallCommands(downloads, endpoint.address, key))

    @Bean
    fun agentVersions(offer: AgentOffer): AgentVersions = AgentVersions(offer.version)
}
