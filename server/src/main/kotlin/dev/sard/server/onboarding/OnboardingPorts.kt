// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.GuardTargets
import dev.sard.server.enrollment.RegistrationGate
import dev.sard.server.selfbackup.OnboardingCompletion
import dev.sard.server.selfbackup.SelfBackups
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.jdbc.core.JdbcTemplate

/**
 * What the self-backup, the enrollment and the REST guard ask the onboarding through (F4b): each asks in its own
 * package, and the answer comes from here, so none of them depends on the wizard.
 */
@Configuration(proxyBeanMethods = false)
class OnboardingPorts {
    @Bean
    fun onboardingCompletion(steps: OnboardingSteps) = OnboardingCompletion(steps::keysConfirmed)

    @Bean
    fun registrationGate(steps: OnboardingSteps) = RegistrationGate(steps::keysConfirmed)

    @Bean
    fun selfBackupGate(selfBackups: SelfBackups): SelfBackupGate = SelfBackupsGate(selfBackups)

    @Bean
    @ConditionalOnMissingBean
    fun guardTargets(jdbc: JdbcTemplate): GuardTargets = JdbcGuardTargets(jdbc)
}
