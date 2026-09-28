// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import jakarta.servlet.Filter
import org.springframework.boot.web.servlet.FilterRegistrationBean
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import tools.jackson.databind.ObjectMapper
import java.time.Clock

private const val API_PATTERN = "/api/v1/*"

/**
 * The administrator session's beans and the two filters that guard /api/v1 (D2, W1b).
 * [OriginGuardFilter] (CSRF, К1/Р5) runs before [SessionAuthFilter] (rule 5): a request
 * with a foreign Origin is refused before authentication is even considered.
 */
@Configuration(proxyBeanMethods = false)
class AuthConfiguration {
    @Bean
    fun sessionStore(clock: Clock) = SessionStore(clock)

    @Bean
    fun loginAttemptTracker(clock: Clock) = LoginAttemptTracker(clock)

    @Bean
    fun originGuardFilterRegistration(objectMapper: ObjectMapper): FilterRegistrationBean<Filter> =
        registration(OriginGuardFilter(objectMapper), order = 1)

    @Bean
    fun sessionAuthFilterRegistration(
        sessionStore: SessionStore,
        objectMapper: ObjectMapper,
    ): FilterRegistrationBean<Filter> = registration(SessionAuthFilter(sessionStore, objectMapper), order = 2)

    private fun registration(
        filter: Filter,
        order: Int,
    ): FilterRegistrationBean<Filter> =
        FilterRegistrationBean(filter).apply {
            urlPatterns = listOf(API_PATTERN)
            this.order = order
        }
}
