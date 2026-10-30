// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.events.SardEventPublisher
import dev.sard.server.extension.SettingsChanged
import org.springframework.boot.context.event.ApplicationReadyEvent
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.context.event.EventListener
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Clock
import java.util.UUID

private const val CLAIM = "delete from admin_password_resets returning tenant_id"
private const val ADMIN_PASSWORD = "admin_password"

/**
 * Tells the listeners of a password reset made while the server was stopped (CORE1 Р32). The command admin-reset runs
 * as a process without listeners and leaves a mark in the database; once the listeners are ready the server takes the
 * mark away and publishes one `admin_password` event for it, with no actor and no object. The mark goes first, so a
 * listener that fails, or a crash, never makes the next start publish it again: at most once (Р6).
 */
class AdminResetAnnouncer(
    private val jdbc: JdbcTemplate,
    private val publisher: SardEventPublisher,
    private val clock: Clock,
) {
    @EventListener(ApplicationReadyEvent::class)
    fun announce() {
        for (tenantId in jdbc.queryForList(CLAIM, UUID::class.java).filterNotNull()) {
            publisher.publish(SettingsChanged(clock.instant(), tenantId, null, ADMIN_PASSWORD, null))
        }
    }
}

/** The bean of [AdminResetAnnouncer]. */
@Configuration(proxyBeanMethods = false)
class AdminResetAnnouncerConfiguration {
    @Bean
    fun adminResetAnnouncer(
        jdbc: JdbcTemplate,
        publisher: SardEventPublisher,
        clock: Clock,
    ) = AdminResetAnnouncer(jdbc, publisher, clock)
}
