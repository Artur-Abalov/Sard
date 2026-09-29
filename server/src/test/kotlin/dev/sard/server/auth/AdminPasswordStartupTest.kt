// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.ClockAutoConfiguration
import dev.sard.server.extension.TenancyAutoConfiguration
import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import tools.jackson.databind.ObjectMapper
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * Rule "Сервер не стартует без пароля администратора не короче 12 символов" (@startup).
 * Runs the real [AdminAuthAutoConfiguration] (the open core's default), so this
 * exercises the same property resolution and bean-creation failure path the full
 * application goes through, without needing a database or gRPC server for a check
 * that does not depend on either.
 */
class AdminPasswordStartupTest {
    // The test JVM's own environment always carries SARD_ADMIN_PASSWORD (build.gradle.kts,
    // for every other test that boots the full context), so "not set" is exercised as an
    // explicit empty property, which the production code treats identically (see below).
    private fun runnerWith(password: String): ApplicationContextRunner =
        ApplicationContextRunner()
            .withConfiguration(
                AutoConfigurations.of(
                    ClockAutoConfiguration::class.java,
                    TenancyAutoConfiguration::class.java,
                    AdminAuthAutoConfiguration::class.java,
                ),
            ).withBean(ObjectMapper::class.java, { ObjectMapper() })
            .withPropertyValues("SARD_ADMIN_PASSWORD=$password")

    private fun failureMessage(password: String): String {
        var message = ""
        runnerWith(password).run { context ->
            assertTrue(context.startupFailure != null)
            message = generateSequence(context.startupFailure) { it.cause }.mapNotNull { it.message }.joinToString("\n")
        }
        return message
    }

    /**
     * Covers both "SARD_ADMIN_PASSWORD is not set" and "set to """: production code
     * (`@Value("\${SARD_ADMIN_PASSWORD:}")`) resolves an absent variable to "" the same
     * way, and the test JVM's own environment always carries it (see [runnerWith]).
     */
    @Test
    fun `an empty SARD_ADMIN_PASSWORD refuses to start and names the variable as unset`() {
        val message = failureMessage("")
        assertTrue(message.contains("SARD_ADMIN_PASSWORD"), message)
        assertTrue(message.contains("not set"), message)
    }

    @Test
    fun `an 11-character password refuses to start and names the 12-character minimum`() {
        val message = failureMessage("short-pw-11")
        assertTrue(message.contains("SARD_ADMIN_PASSWORD"), message)
        assertTrue(message.contains("12"), message)
    }

    @Test
    fun `the failure never contains the rejected password`() {
        assertFalse(failureMessage("short-pw-11").contains("short-pw-11"))
    }

    @Test
    fun `a 12-character password starts`() {
        runnerWith("exactly-12ch").run { context -> assertTrue(context.startupFailure == null) }
    }

    @Test
    fun `12 Cyrillic code points start even though they are 24 bytes of UTF-8`() {
        runnerWith("паролькирилл").run { context -> assertTrue(context.startupFailure == null) }
    }

    @Test
    fun `11 non-ASCII code points refuse to start, 22 bytes notwithstanding`() {
        val message = failureMessage("парольнекор")
        assertTrue(message.contains("SARD_ADMIN_PASSWORD"), message)
        assertTrue(message.contains("12"), message)
    }
}
