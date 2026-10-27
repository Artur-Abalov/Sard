// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import org.slf4j.LoggerFactory
import org.springframework.context.SmartLifecycle
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Date
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.time.YearMonth
import java.time.ZoneOffset
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

private val log = LoggerFactory.getLogger(StepLogPartitions::class.java)

/**
 * Monthly partitions of `step_logs` (ADR 0013), created ahead by the server through a function of the database
 * (sard_app may not create a table): at start, for the
 * clock's month and [monthsAhead] after it, then every [interval]. A line whose month has no
 * partition fails its chunk, so the months ahead are the margin for a server that was down.
 * Dropping expired partitions is `retention.StepLogRetention`'s, by `sard.retention.step-logs`.
 */
class StepLogPartitions(
    private val jdbc: JdbcTemplate,
    private val clock: Clock,
    private val monthsAhead: Int,
    private val interval: Duration,
) : SmartLifecycle {
    private var executor: ScheduledExecutorService? = null

    /** Creates the missing partitions from [now]'s month on; returns their names. */
    fun ensure(
        now: Instant,
        monthsAhead: Int,
    ): List<String> {
        val first = YearMonth.from(now.atOffset(ZoneOffset.UTC))
        return (0..monthsAhead).map { first.plusMonths(it.toLong()) }.mapNotNull(::create)
    }

    override fun start() {
        ensure(clock.instant(), monthsAhead).forEach { log.info("created log partition {}", it) }
        val scheduler = Executors.newSingleThreadScheduledExecutor(::daemon)
        scheduler.scheduleWithFixedDelay(::check, interval.toMillis(), interval.toMillis(), TimeUnit.MILLISECONDS)
        executor = scheduler
    }

    private fun daemon(task: Runnable) = Thread(task, "sard-log-partitions").apply { isDaemon = true }

    override fun stop() {
        executor?.shutdownNow()
        executor = null
    }

    override fun isRunning() = executor != null

    /** The periodic check. Never throws: a thrown exception would cancel every later run of the periodic task. */
    fun check() {
        runCatching { ensure(clock.instant(), monthsAhead) }
            .onSuccess { created -> created.forEach { log.info("created log partition {}", it) } }
            .onFailure { log.warn("Log partitions not checked; retrying at the next check", it) }
    }

    /**
     * The name of the partition of [month] this call made, or null when it existed: `create_step_log_partition`
     * does it as sard_owner (DB2, ADR 0069) and serialises servers that start together on one advisory lock.
     */
    private fun create(month: YearMonth): String? =
        jdbc.queryForObject("select create_step_log_partition(?)", String::class.java, Date.valueOf(month.atDay(1)))
}
