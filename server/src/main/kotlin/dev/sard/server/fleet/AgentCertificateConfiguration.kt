// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import org.springframework.beans.factory.annotation.Qualifier
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.security.SecureRandom
import java.time.Clock
import java.time.Duration

@Configuration(proxyBeanMethods = false)
class AgentCertificateConfiguration {
    @Bean
    fun agentCertificates(sessions: TenantSessions) = AgentCertificates(sessions)

    @Bean
    fun agentCertificateCheck(
        sessions: TenantSessions,
        clock: Clock,
    ) = AgentCertificateCheck(sessions, clock, UuidV7(clock, SecureRandom()))

    /** Checks the expiry of the agents' certificates every `sard.pki.agent-certificate-check-interval` (П35). */
    @Bean
    fun agentCertificateLoop(
        @Qualifier("agentCertificateCheckInterval") interval: Duration,
        check: AgentCertificateCheck,
    ) = AgentCertificateLoop(interval, check::tick)
}
