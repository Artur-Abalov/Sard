// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.pki.CertificateAuthority
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.security.SecureRandom
import java.time.Clock

@Configuration(proxyBeanMethods = false)
class EnrollmentConfiguration {
    @Bean
    fun enrollmentTokens(
        sessions: TenantSessions,
        ca: CertificateAuthority,
        clock: Clock,
    ) = EnrollmentTokens(sessions, ca, clock, SecureRandom(), UuidV7(clock, SecureRandom()))
}
