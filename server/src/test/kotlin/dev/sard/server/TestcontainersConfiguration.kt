// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import org.springframework.beans.factory.InitializingBean
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.boot.testcontainers.service.connection.ServiceConnection
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.DependsOn
import org.springframework.jdbc.core.JdbcTemplate
import org.testcontainers.postgresql.PostgreSQLContainer
import org.testcontainers.utility.DockerImageName
import java.time.Instant

/**
 * A real PostgreSQL in a throwaway container; no external services. The onboarding is complete in it from the
 * start (F4b): the tests that issue tokens or create sources are not about the first start. A test of the first
 * start takes the step away and puts it back ([withOnboardingIncomplete]).
 */
@TestConfiguration(proxyBeanMethods = false)
class TestcontainersConfiguration {
    @Bean
    @ServiceConnection
    fun postgres(): PostgreSQLContainer = PostgreSQLContainer(DockerImageName.parse("postgres:18-alpine"))

    @Bean
    @DependsOn("flywayInitializer")
    fun completedOnboarding(jdbc: JdbcTemplate) = InitializingBean { completeOnboarding(jdbc) }
}

/** The step keys_confirmed done, as after the owner confirmed the key copy. */
fun completeOnboarding(jdbc: JdbcTemplate) {
    jdbc.update(
        "insert into onboarding_steps (step, completed_at) values ('keys_confirmed', ?) on conflict do nothing",
        java.sql.Timestamp.from(Instant.now()),
    )
}

/** Runs [block] with the onboarding not complete, and completes it again afterwards. */
fun <T> withOnboardingIncomplete(
    jdbc: JdbcTemplate,
    block: () -> T,
): T {
    jdbc.update("delete from onboarding_steps where step = 'keys_confirmed'")
    try {
        return block()
    } finally {
        completeOnboarding(jdbc)
    }
}
