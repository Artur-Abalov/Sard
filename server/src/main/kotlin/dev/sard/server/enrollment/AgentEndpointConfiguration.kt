// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.pki.CaStartPrecondition
import dev.sard.server.pki.PkiProperties
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.time.Duration

/**
 * `sard.agent.endpoint` (SARD_AGENT_ENDPOINT): empty means unset (decision 5).
 * `sard.agent.heartbeat-interval` (SARD_AGENT_HEARTBEAT_INTERVAL): how often agents send
 * Heartbeat, handed out by Register (S4a); the same setting decides when an agent is offline (S5a).
 */
@ConfigurationProperties("sard.agent")
data class AgentEndpointProperties(
    val endpoint: String = "",
    val heartbeatInterval: Duration = Duration.ofSeconds(DEFAULT_HEARTBEAT_SECONDS),
) {
    init {
        require(heartbeatInterval.isPositive) { "sard.agent.heartbeat-interval must be positive" }
    }

    private companion object {
        /** The agent's own fallback when Register sends none (agent/internal/transport DefaultHeartbeat). */
        const val DEFAULT_HEARTBEAT_SECONDS = 30L
    }
}

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

    /** Resolving [endpoint] validates it; the CA waits for that, so a refused address leaves no CA (ADR 0052). */
    @Bean
    fun agentEndpointBeforeCa(endpoint: AgentEndpoint) = CaStartPrecondition { endpoint.address }
}
