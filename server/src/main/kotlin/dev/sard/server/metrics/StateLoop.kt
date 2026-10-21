// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import org.springframework.context.SmartLifecycle
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

/**
 * Runs [tick] at the start and then [interval] after the end of each tick, on one thread of its own: ticks never
 * overlap, a slow one delays the next and nothing else, and a scrape never waits for either (МР6). The first tick is
 * in the background, so a database that is slow to answer does not delay the readiness of the server.
 */
class StateLoop(
    private val interval: Duration,
    private val tick: () -> Unit,
) : SmartLifecycle {
    @Volatile
    private var executor: ScheduledExecutorService? = null

    override fun start() {
        val scheduler = Executors.newSingleThreadScheduledExecutor(::daemon)
        scheduler.scheduleWithFixedDelay(::safeTick, 0, interval.toMillis(), TimeUnit.MILLISECONDS)
        executor = scheduler
    }

    private fun daemon(task: Runnable) = Thread(task, "sard-metrics-state").apply { isDaemon = true }

    override fun stop() {
        executor?.shutdownNow()
        executor = null
    }

    override fun isRunning() = executor != null

    /** Never throws: a thrown exception would cancel every later run of the periodic task. */
    private fun safeTick() {
        runCatching(tick)
    }
}
