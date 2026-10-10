// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import org.springframework.beans.factory.InitializingBean
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.DependsOn
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Clock

/**
 * The step ca already done when the server starts, for the tests of what follows it (the built-in agent, say):
 * the owner confirmed the CA in the wizard before this test began. Written after the CA is opened, because an
 * empty CA directory with a step ca done is a refusal (Р19).
 */
@TestConfiguration(proxyBeanMethods = false)
class ConfirmedCaStep {
    @Bean
    @DependsOn("certificateAuthority")
    fun confirmedCaStep(
        jdbc: JdbcTemplate,
        clock: Clock,
    ) = InitializingBean { JdbcOnboardingSteps(jdbc).confirmCa(clock.instant()) }
}
