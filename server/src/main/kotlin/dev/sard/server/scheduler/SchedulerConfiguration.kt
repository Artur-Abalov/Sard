// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.runs.Runs
import dev.sard.server.runs.StepsQueued
import org.slf4j.LoggerFactory
import org.springframework.beans.factory.ObjectProvider
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.SmartLifecycle
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.security.SecureRandom
import java.time.Clock
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

private val log = LoggerFactory.getLogger(SchedulerLoop::class.java)

private const val INTERVAL_SECONDS = 10L
private const val SPACING_SECONDS = 10L
private const val ALERT_THRESHOLD = 3
private const val BATCH = 500

/**
 * `sard.scheduler.*` (F3a). [interval] must stay well under a minute, the finest cron step: a tick
 * later than a whole interval of a schedule finds two passed fires and reads them as a downtime.
 */
@ConfigurationProperties("sard.scheduler")
data class SchedulerProperties(
    val interval: Duration = Duration.ofSeconds(INTERVAL_SECONDS),
    val catchUpSpacing: Duration = Duration.ofSeconds(SPACING_SECONDS),
    val skipAlertThreshold: Int = ALERT_THRESHOLD,
    val batch: Int = BATCH,
) {
    fun settings(): SchedulerSettings {
        require(interval.isPositive) { "sard.scheduler.interval must be positive" }
        require(!catchUpSpacing.isNegative) { "sard.scheduler.catch-up-spacing must not be negative" }
        require(skipAlertThreshold >= 1) { "sard.scheduler.skip-alert-threshold must be at least 1" }
        require(batch >= 1) { "sard.scheduler.batch must be at least 1" }
        return SchedulerSettings(catchUpSpacing, skipAlertThreshold, batch)
    }
}

/** Runs [tick] every [interval] on one thread; ticks never overlap. */
class SchedulerLoop(
    private val interval: Duration,
    private val tick: () -> Unit,
) : SmartLifecycle {
    @Volatile
    private var executor: ScheduledExecutorService? = null

    override fun start() {
        val scheduler = Executors.newSingleThreadScheduledExecutor(::daemon)
        scheduler.scheduleWithFixedDelay(::safeTick, interval.toMillis(), interval.toMillis(), TimeUnit.MILLISECONDS)
        executor = scheduler
    }

    override fun stop() {
        executor?.shutdownNow()
        executor = null
    }

    override fun isRunning() = executor != null

    private fun daemon(task: Runnable) = Thread(task, "sard-scheduler").apply { isDaemon = true }

    /** Never throws: a thrown exception would cancel every later run of the periodic task. */
    private fun safeTick() {
        runCatching(tick).onFailure { log.warn("Scheduler tick failed; retrying at the next one", it) }
    }
}

@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(SchedulerProperties::class)
class SchedulerConfiguration {
    @Bean
    fun schedulerSettings(properties: SchedulerProperties) = properties.settings()

    @Bean
    fun schedules(
        sessions: TenantSessions,
        clock: Clock,
    ) = Schedules(sessions, clock, UuidV7(clock, SecureRandom()))

    @Bean
    fun scheduler(
        sessions: TenantSessions,
        clock: Clock,
        runs: Runs,
        queued: ObjectProvider<StepsQueued>,
        settings: SchedulerSettings,
    ): Scheduler {
        val listener = queued.getIfUnique { StepsQueued.NONE }
        return Scheduler(sessions, clock, runs, UuidV7(clock, SecureRandom()), listener, settings)
    }

    @Bean
    fun schedulerLoop(
        scheduler: Scheduler,
        properties: SchedulerProperties,
    ) = SchedulerLoop(properties.interval, scheduler::tick)
}
