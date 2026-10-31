// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.ClockAutoConfiguration
import dev.sard.server.api.SessionApi
import dev.sard.server.events.SardEventPublisher
import dev.sard.server.events.SettingsEvents
import dev.sard.server.events.SubjectActors
import dev.sard.server.extension.AccessPolicyAutoConfiguration
import dev.sard.server.extension.SessionExpired
import dev.sard.server.extension.TenancyAutoConfiguration
import dev.sard.server.extension.TenantResolver
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean
import org.springframework.context.annotation.Bean
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Clock

/**
 * The administrator session's beans (D2, W1b). The chain that guards /api/v1 with them is
 * [CoreSecurityAutoConfiguration]'s (ADR 0071).
 * Every bean is `@ConditionalOnMissingBean`, so an enterprise starter (SSO, say) that
 * declares `@AutoConfiguration(before = [AdminAuthAutoConfiguration::class])` and
 * registers its own [SessionApi] replaces password sign-in outright: neither the stored
 * administrator nor the default [SessionApi] is created, and the first-start wizard sees
 * [ExternalAdminSetup] instead (ADR 0021's seam, F4a; ADR 0071).
 */
@AutoConfiguration(
    after = [ClockAutoConfiguration::class, TenancyAutoConfiguration::class, AccessPolicyAutoConfiguration::class],
)
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
        settings: SettingsEvents,
    ): AdminSetup = StoredAdminSetup(administrators, passwordHasher, clock, settings)

    @Bean
    @ConditionalOnMissingBean(AdminSetup::class)
    fun externalAdminSetup(): AdminSetup = ExternalAdminSetup

    @Bean
    @ConditionalOnMissingBean
    fun sessionStore(
        clock: Clock,
        publisher: SardEventPublisher,
    ) = SessionStore(clock) { session, reason ->
        val event = SessionExpired(clock.instant(), session.tenantId, session.subject, session.clientAddress, reason)
        publisher.publish(event)
    }

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
        publisher: SardEventPublisher,
        settings: SettingsEvents,
        actors: SubjectActors,
    ): SessionApi =
        SessionApiImpl(
            administrators,
            passwordHasher,
            sessionStore,
            attemptTracker,
            tenantResolver,
            clock,
            publisher,
            settings,
            actors,
        )
}
