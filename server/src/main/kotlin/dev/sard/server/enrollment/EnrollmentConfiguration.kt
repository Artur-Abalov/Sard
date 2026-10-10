// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.pki.CertificateAuthority
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.jdbc.core.JdbcTemplate
import java.security.SecureRandom
import java.time.Clock

@Configuration(proxyBeanMethods = false)
class EnrollmentConfiguration {
    @Bean
    fun enrollmentTokens(
        sessions: TenantSessions,
        ca: CertificateAuthority,
        clock: Clock,
        endpoint: AgentEndpoint,
    ) = EnrollmentTokens(sessions, ca, clock, SecureRandom(), UuidV7(clock, SecureRandom()), endpoint)

    /** Tokens carry the fingerprint of the CA they were issued under (F4a, OQ-191). */
    @Bean
    fun tokensRevokedOnCaReplacement(
        jdbc: JdbcTemplate,
        clock: Clock,
    ) = TokensRevokedOnCaReplacement(jdbc, clock)

    @Bean
    fun enrollment(
        sessions: TenantSessions,
        tokens: EnrollmentTokens,
        ca: CertificateAuthority,
        clock: Clock,
    ) = Enrollment(sessions, tokens, ca, clock, UuidV7(clock, SecureRandom()))
}
