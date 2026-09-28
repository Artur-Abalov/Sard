// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.ClockAutoConfiguration
import dev.sard.server.api.SessionApi
import dev.sard.server.extension.TenancyAutoConfiguration
import dev.sard.server.extension.TenantResolver
import jakarta.servlet.Filter
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean
import org.springframework.boot.web.servlet.FilterRegistrationBean
import org.springframework.context.annotation.Bean
import tools.jackson.databind.ObjectMapper
import java.time.Clock

private const val API_PATTERN = "/api/v1/*"

/**
 * The administrator session's beans and the two filters that guard /api/v1 (D2, W1b).
 * Every bean is `@ConditionalOnMissingBean`, so an enterprise starter (SSO, say) that
 * declares `@AutoConfiguration(before = [AdminAuthAutoConfiguration::class])` and
 * registers its own [SessionApi] replaces password sign-in outright: neither
 * [AdminPasswordAuthenticator] nor the default [SessionApi] is created, and the
 * open core's own SARD_ADMIN_PASSWORD requirement never applies (ADR 0020's seam).
 * [OriginGuardFilter] (CSRF, К1/Р5) runs before [SessionAuthFilter] (rule 5): a request
 * with a foreign Origin is refused before authentication is even considered.
 */
@AutoConfiguration(after = [ClockAutoConfiguration::class, TenancyAutoConfiguration::class])
class AdminAuthAutoConfiguration {
    @Bean
    @ConditionalOnMissingBean(SessionApi::class)
    fun adminPasswordAuthenticator(
        @Value("\${SARD_ADMIN_PASSWORD:}") rawPassword: String,
    ) = AdminPasswordAuthenticator(rawPassword)

    @Bean
    @ConditionalOnMissingBean
    fun sessionStore(clock: Clock) = SessionStore(clock)

    @Bean
    @ConditionalOnMissingBean
    fun loginAttemptTracker(clock: Clock) = LoginAttemptTracker(clock)

    @Bean
    @ConditionalOnMissingBean(SessionApi::class)
    fun sessionApi(
        passwordAuthenticator: AdminPasswordAuthenticator,
        sessionStore: SessionStore,
        attemptTracker: LoginAttemptTracker,
        tenantResolver: TenantResolver,
    ): SessionApi = SessionApiImpl(passwordAuthenticator, sessionStore, attemptTracker, tenantResolver)

    @Bean
    @ConditionalOnMissingBean(name = ["originGuardFilterRegistration"])
    fun originGuardFilterRegistration(objectMapper: ObjectMapper): FilterRegistrationBean<Filter> =
        registration(OriginGuardFilter(objectMapper), order = 1)

    @Bean
    @ConditionalOnMissingBean(name = ["sessionAuthFilterRegistration"])
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
