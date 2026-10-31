// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.freshness

import dev.sard.server.loop.PeriodicTask
import dev.sard.server.loop.Timing
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import org.slf4j.LoggerFactory
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.SmartLifecycle
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.security.SecureRandom
import java.time.Clock
import java.time.Duration

private val log = LoggerFactory.getLogger(FreshnessLoop::class.java)

private const val INTERVAL_SECONDS = 60L

/** `sard.freshness.*` (F6): how often the overdue check runs; an episode opens at most this late. */
@ConfigurationProperties("sard.freshness")
data class FreshnessProperties(
    val interval: Duration = Duration.ofSeconds(INTERVAL_SECONDS),
) {
    fun checkInterval(): Duration {
        require(interval.isPositive) { "sard.freshness.interval must be positive" }
        return interval
    }
}

/** Runs [tick] every [interval] on one thread; ticks never overlap. */
class FreshnessLoop(
    interval: Duration,
    tick: () -> Unit,
) : SmartLifecycle by PeriodicTask(
        name = "sard-freshness",
        timing = Timing(interval),
        onFailure = { log.warn("Freshness check failed; retrying at the next one", it) },
        action = tick,
    )

@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(FreshnessProperties::class)
class FreshnessConfiguration {
    @Bean
    fun freshnessCheck(
        sessions: TenantSessions,
        clock: Clock,
    ) = FreshnessCheck(sessions, clock, UuidV7(clock, SecureRandom()))

    @Bean
    fun freshnessLoop(
        properties: FreshnessProperties,
        check: FreshnessCheck,
    ) = FreshnessLoop(properties.checkInterval(), check::tick)
}
