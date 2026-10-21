// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import dev.sard.server.notify.NotifyProperties
import dev.sard.server.persistence.TenantSessions
import io.micrometer.core.instrument.MeterRegistry
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Clock
import java.time.Duration

/** How long the drop of a log partition waits for a transaction that holds it, before the pass logs it and goes on. */
private val PARTITION_LOCK_TIMEOUT: Duration = Duration.ofSeconds(10)

/** The settings of `sard.retention.*`; a value the cleaning cannot work with stops the start naming the property. */
@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(RetentionProperties::class, NotifyProperties::class)
class RetentionSettingsConfiguration {
    @Bean
    fun retentionSettings(
        properties: RetentionProperties,
        notify: NotifyProperties,
    ): RetentionSettings = properties.settings(notify.ttl)
}

@Configuration(proxyBeanMethods = false)
class RetentionConfiguration {
    @Bean
    fun retentionMetrics(meters: MeterRegistry): RetentionMetrics = MicrometerRetentionMetrics(meters)

    @Bean
    fun stepLogRetention(jdbc: JdbcTemplate) = StepLogRetention(jdbc, PARTITION_LOCK_TIMEOUT)

    @Bean
    fun retentionPass(
        sessions: TenantSessions,
        logs: StepLogRetention,
        clock: Clock,
        settings: RetentionSettings,
        metrics: RetentionMetrics,
    ) = RetentionPass(sessions, logs, clock, settings, metrics)

    @Bean
    fun retentionLoop(
        settings: RetentionSettings,
        pass: RetentionPass,
    ) = RetentionLoop(settings.interval, pass::run)
}
