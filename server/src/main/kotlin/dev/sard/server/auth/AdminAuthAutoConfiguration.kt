// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.ClockAutoConfiguration
import dev.sard.server.api.SessionApi
import dev.sard.server.extension.TenancyAutoConfiguration
import dev.sard.server.extension.TenantResolver
import jakarta.servlet.Filter
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean
import org.springframework.boot.web.servlet.FilterRegistrationBean
import org.springframework.context.annotation.Bean
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.time.Clock

private const val API_PATTERN = "/api/v1/*"

/**
 * The administrator session's beans and the two filters that guard /api/v1 (D2, W1b).
 * Every bean is `@ConditionalOnMissingBean`, so an enterprise starter (SSO, say) that
 * declares `@AutoConfiguration(before = [AdminAuthAutoConfiguration::class])` and
 * registers its own [SessionApi] replaces password sign-in outright: neither the stored
 * administrator nor the default [SessionApi] is created, and the first-start wizard sees
 * [ExternalAdminSetup] instead (ADR 0021's seam, F4a).
 * [OriginGuardFilter] (CSRF, К1/Р5) runs before [SessionAuthFilter] (rule 5): a request
 * with a foreign Origin is refused before authentication is even considered.
 */
@AutoConfiguration(after = [ClockAutoConfiguration::class, TenancyAutoConfiguration::class])
class AdminAuthAutoConfiguration {
    @Bean
    @ConditionalOnMissingBean(SessionApi::class)
    fun administrators(jdbc: JdbcTemplate): Administrators = JdbcAdministrators(jdbc)

    @Bean
    @ConditionalOnMissingBean(SessionApi::class)
    fun passwordHasher() = PasswordHasher()

    /** Declared before [externalAdminSetup]: that one applies exactly when this one does not. */
    @Bean
    @ConditionalOnMissingBean(SessionApi::class)
    fun adminSetup(
        administrators: Administrators,
        passwordHasher: PasswordHasher,
        clock: Clock,
    ): AdminSetup = StoredAdminSetup(administrators, passwordHasher, clock)

    @Bean
    @ConditionalOnMissingBean(AdminSetup::class)
    fun externalAdminSetup(): AdminSetup = ExternalAdminSetup

    @Bean
    @ConditionalOnMissingBean
    fun sessionStore(clock: Clock) = SessionStore(clock)

    @Bean
    @ConditionalOnMissingBean
    fun loginAttemptTracker(clock: Clock) = LoginAttemptTracker(clock)

    @Bean
    @ConditionalOnMissingBean(SessionApi::class)
    fun sessionApi(
        administrators: Administrators,
        passwordHasher: PasswordHasher,
        sessionStore: SessionStore,
        attemptTracker: LoginAttemptTracker,
        tenantResolver: TenantResolver,
        clock: Clock,
    ): SessionApi = SessionApiImpl(administrators, passwordHasher, sessionStore, attemptTracker, tenantResolver, clock)

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
