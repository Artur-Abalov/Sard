// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.runs.Runs
import dev.sard.server.runs.StepsQueued
import io.micrometer.core.instrument.Counter
import io.micrometer.core.instrument.Gauge
import io.micrometer.core.instrument.MeterRegistry
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
import java.time.ZoneId
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicLong

private val log = LoggerFactory.getLogger(SchedulerLoop::class.java)

private const val INTERVAL_SECONDS = 10L
private const val SPACING_SECONDS = 10L
private const val ALERT_THRESHOLD = 3
private const val BATCH = 500
private const val MILLIS_PER_SECOND = 1000.0

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
        valid(interval.isPositive, "interval must be positive")
        valid(!catchUpSpacing.isNegative, "catch-up-spacing must not be negative")
        valid(skipAlertThreshold >= 1, "skip-alert-threshold must be at least 1")
        valid(batch >= 1, "batch must be at least 1")
        return SchedulerSettings(catchUpSpacing, skipAlertThreshold, batch)
    }

    private fun valid(
        holds: Boolean,
        rule: String,
    ) = require(holds) { "sard.scheduler.$rule" }
}

/** Micrometer meters of the scheduler: `sard.scheduler.fires{kind,outcome}` and `sard.scheduler.lag.seconds`. */
class MicrometerSchedulerMetrics(
    private val meters: MeterRegistry,
) : SchedulerMetrics {
    private val lagMillis = AtomicLong()

    init {
        Gauge.builder("sard.scheduler.lag.seconds", lagMillis) { it.get() / MILLIS_PER_SECOND }.register(meters)
    }

    override fun fired(
        kind: FireKind,
        outcome: FireOutcome,
    ) = Counter
        .builder("sard.scheduler.fires")
        .tag("kind", kind.stored)
        .tag("outcome", outcome.stored)
        .register(meters)
        .increment()

    override fun lag(behind: Duration) = lagMillis.set(behind.toMillis())
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
    fun schedulePreviews(clock: Clock) = SchedulePreviews(clock, ZoneId.systemDefault())

    @Bean
    fun scheduler(
        sessions: TenantSessions,
        clock: Clock,
        runs: Runs,
        queued: ObjectProvider<StepsQueued>,
        settings: SchedulerSettings,
        meters: MeterRegistry,
    ): Scheduler {
        val listener = queued.getIfUnique { StepsQueued.NONE }
        val metrics = MicrometerSchedulerMetrics(meters)
        return Scheduler(sessions, clock, runs, UuidV7(clock, SecureRandom()), listener, settings, metrics)
    }

    @Bean
    fun schedulerLoop(
        scheduler: Scheduler,
        properties: SchedulerProperties,
    ) = SchedulerLoop(properties.interval, scheduler::tick)
}
