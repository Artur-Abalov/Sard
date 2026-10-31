// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import org.springframework.boot.EnvironmentPostProcessor
import org.springframework.boot.SpringApplication
import org.springframework.core.env.ConfigurableEnvironment
import org.springframework.core.env.MapPropertySource

private const val EXCLUDE = "spring.autoconfigure.exclude"
private const val SOURCE = "sardNoDefaultUser"
private const val DEFAULT_USER = "org.springframework.boot.security.autoconfigure.UserDetailsServiceAutoConfiguration"

/**
 * Keeps Spring Boot from creating its default user with a generated password, which it does whenever Spring Security
 * is on the class path and the application declares no user store (CORE1 Р34, ADR 0071). The chain of the core has no
 * users: the session of the core and the sign-in of an extension authenticate. Done for every application built on the
 * core, not only for the one with the core's main class; what an operator excludes besides is kept.
 */
class NoDefaultUser : EnvironmentPostProcessor {
    override fun postProcessEnvironment(
        environment: ConfigurableEnvironment,
        application: SpringApplication,
    ) {
        val excluded =
            environment
                .getProperty(EXCLUDE)
                .orEmpty()
                .split(',')
                .map { it.trim() }
                .filter { it.isNotEmpty() }
        val all = (excluded + DEFAULT_USER).distinct().joinToString(",")
        environment.propertySources.addFirst(MapPropertySource(SOURCE, mapOf(EXCLUDE to all)))
    }
}
