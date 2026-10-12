// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import dev.sard.server.auth.JdbcAdministrators
import dev.sard.server.auth.PasswordHasher
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.ApplicationRunner
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Clock

/**
 * The administrator the wizard would have made, without the wizard: `sard.test.admin-password` is the password,
 * stored as the real Argon2id hash when the context is up. For the tests that are about something other than
 * the first start; those about the first start run the wizard.
 */
@TestConfiguration(proxyBeanMethods = false)
class SeededAdministrator {
    @Bean
    fun seedAdministrator(
        jdbc: JdbcTemplate,
        clock: Clock,
        @Value("\${sard.test.admin-password}") password: String,
    ) = ApplicationRunner {
        JdbcAdministrators(jdbc).create(PasswordHasher().hash(password), clock.instant())
    }
}
