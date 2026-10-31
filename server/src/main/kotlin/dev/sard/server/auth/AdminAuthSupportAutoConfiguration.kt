// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean

/** What the events of the core need from the session: the sweep that ends forgotten sessions. */
@EnableConfigurationProperties(SessionProperties::class)
@AutoConfiguration(after = [AdminAuthAutoConfiguration::class])
class AdminAuthSupportAutoConfiguration {
    @Bean
    @ConditionalOnMissingBean
    fun sessionSweep(
        properties: SessionProperties,
        sessionStore: SessionStore,
    ) = SessionSweep(properties.interval(), sessionStore)
}
