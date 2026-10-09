// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.ClockAutoConfiguration
import dev.sard.server.api.OnboardingApi
import dev.sard.server.api.toInfo
import dev.sard.server.auth.AdminAuthAutoConfiguration
import dev.sard.server.auth.AdminSetup
import dev.sard.server.auth.LoginAttemptTracker
import dev.sard.server.auth.SessionStore
import dev.sard.server.extension.TenancyAutoConfiguration
import dev.sard.server.extension.TenantResolver
import dev.sard.server.pki.CaLedger
import dev.sard.server.pki.CertificateAuthority
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.DependsOn
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Clock

/**
 * The first-start wizard (F4a): the setup code of this process, the setup sessions, the steps in the database, and
 * the ledger the CA directory is checked against. The setup code generator is replaceable (tests fix the code).
 */
@AutoConfiguration(after = [ClockAutoConfiguration::class, TenancyAutoConfiguration::class, AdminAuthAutoConfiguration::class])
class OnboardingAutoConfiguration {
    @Bean
    @ConditionalOnMissingBean
    fun setupCodeGenerator(): SetupCodeGenerator = RandomSetupCodeGenerator()

    @Bean
    fun setupCodes(
        clock: Clock,
        generator: SetupCodeGenerator,
    ) = SetupCodes(clock, generator)

    @Bean
    fun setupSessions(clock: Clock) = SetupSessions(clock)

    @Bean
    fun onboardingSteps(jdbc: JdbcTemplate): OnboardingSteps = JdbcOnboardingSteps(jdbc)

    /** The CA is opened with it, so the migrations of the database must have run first. */
    @Bean
    @DependsOn("flywayInitializer")
    fun caLedger(
        jdbc: JdbcTemplate,
        clock: Clock,
    ): CaLedger = JdbcCaLedger(jdbc, clock)

    @Bean
    fun setupCodeAnnouncer(
        adminSetup: AdminSetup,
        codes: SetupCodes,
    ) = SetupCodeAnnouncer(adminSetup, codes)

    @Bean
    fun onboardingApi(
        clock: Clock,
        adminSetup: AdminSetup,
        steps: OnboardingSteps,
        codes: SetupCodes,
        setupSessions: SetupSessions,
        adminSessions: SessionStore,
        tenantResolver: TenantResolver,
        ledger: CaLedger,
        ca: CertificateAuthority,
    ): OnboardingApi =
        OnboardingService(
            clock,
            adminSetup,
            steps,
            codes,
            setupSessions,
            // Entering the code has its own failure counter, apart from the one of sign-in (Р4).
            LoginAttemptTracker(clock),
            adminSessions,
            tenantResolver,
            ledger::usage,
            ca::toInfo,
        )
}
