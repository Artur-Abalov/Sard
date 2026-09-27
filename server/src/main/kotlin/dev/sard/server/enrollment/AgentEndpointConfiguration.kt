// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.pki.PkiProperties
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration

/** `sard.agent.endpoint` (SARD_AGENT_ENDPOINT): empty means unset (decision 5). */
@ConfigurationProperties("sard.agent")
data class AgentEndpointProperties(
    val endpoint: String = "",
)

/**
 * Resolves the address agents dial at startup, so a host outside the server certificate stops
 * the server here instead of failing every agent's TLS handshake later (decision 5).
 */
@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(AgentEndpointProperties::class)
class AgentEndpointConfiguration {
    @Bean
    fun agentEndpoint(
        properties: AgentEndpointProperties,
        pki: PkiProperties,
        @Value("\${spring.grpc.server.port}") grpcPort: Int,
    ): AgentEndpoint = AgentEndpointResolver.resolve(properties.endpoint, pki.serverNames, grpcPort)
}
