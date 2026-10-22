// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.prometheus

import org.springframework.context.SmartLifecycle
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

private const val STOP_PATIENCE_SECONDS = 10L

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

    /**
     * Interrupts a tick that is running and waits for it for [STOP_PATIENCE_SECONDS] at most, so that when this returns
     * no tick runs and none will start, unless the tick ignores the interrupt (a JDBC call may) for longer than that.
     */
    override fun stop() {
        executor?.let {
            it.shutdownNow()
            it.awaitTermination(STOP_PATIENCE_SECONDS, TimeUnit.SECONDS)
        }
        executor = null
    }

    override fun isRunning() = executor != null

    /** Never throws: a thrown exception would cancel every later run of the periodic task. */
    private fun safeTick() {
        runCatching(tick)
    }
}
