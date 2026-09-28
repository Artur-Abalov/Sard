// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.EnrollmentServiceGrpc
import dev.sard.server.persistence.TenantSessions
import io.grpc.health.v1.HealthGrpc
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.core.Ordered
import org.springframework.core.annotation.Order
import org.springframework.grpc.server.GlobalServerInterceptor
import java.time.Clock

/**
 * The services reachable without a client certificate (ADR 0009): enrollment, which
 * issues the first one, and the health check. Changing this set is a review item.
 */
val UNAUTHENTICATED_SERVICES: Set<String> =
    setOf(EnrollmentServiceGrpc.SERVICE_NAME, HealthGrpc.SERVICE_NAME)

@Configuration(proxyBeanMethods = false)
class AgentAuthConfiguration {
    @Bean
    fun agentAuthenticator(
        sessions: TenantSessions,
        clock: Clock,
    ) = AgentAuthenticator(AgentCertificateStandings(sessions), clock)

    /** Global: applies to every service the server binds, first in the chain. */
    @Bean
    @GlobalServerInterceptor
    @Order(Ordered.HIGHEST_PRECEDENCE)
    fun agentAuthInterceptor(authenticator: AgentAuthenticator): AgentAuthInterceptor =
        AgentAuthInterceptor(authenticator, UNAUTHENTICATED_SERVICES)
}
