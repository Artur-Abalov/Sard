// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import dev.sard.server.notify.NotifyProperties
import dev.sard.server.persistence.TenantSessions
import io.micrometer.core.instrument.MeterRegistry
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.context.annotation.DependsOn
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Clock
import java.time.Duration

/** The settings of `sard.retention.*`; a value the cleaning cannot work with stops the start naming the property. */
@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(RetentionProperties::class, NotifyProperties::class)
class RetentionSettingsConfiguration {
    @Bean
    fun retentionSettings(
        properties: RetentionProperties,
        notify: NotifyProperties,
        floor: VerificationEvidenceFloor,
    ): RetentionSettings = properties.settings(notify.ttl, floor.term())
}

/** The shortest term the database lets the evidence of a verification be kept for, fixed by a migration (DB2). */
fun interface VerificationEvidenceFloor {
    fun term(): Duration
}

private const val EVIDENCE_FLOOR = "select min_days from retention_floors where name = 'verification_evidence'"

@Configuration(proxyBeanMethods = false)
class RetentionConfiguration {
    /** After the migrations are checked: the floor of the evidence term is a row of the database (DB2). */
    @Bean
    @DependsOn("flywayInitializer")
    fun verificationEvidenceFloor(jdbc: JdbcTemplate): VerificationEvidenceFloor {
        val days = jdbc.queryForObject(EVIDENCE_FLOOR, Int::class.javaObjectType)
        val term = Duration.ofDays(checkNotNull(days).toLong())
        return VerificationEvidenceFloor { term }
    }

    @Bean
    fun retentionMetrics(meters: MeterRegistry): RetentionMetrics = MicrometerRetentionMetrics(meters)

    @Bean
    fun stepLogRetention(jdbc: JdbcTemplate) = StepLogRetention(jdbc)

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
