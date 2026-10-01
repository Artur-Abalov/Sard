// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import org.slf4j.LoggerFactory
import org.springframework.context.SmartLifecycle
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

private val log = LoggerFactory.getLogger(NotificationLoop::class.java)

/**
 * Runs [tick] every [interval] on one thread, and soon after [wake] (a run just finished). Ticks
 * never overlap: the scheduler has a single thread.
 */
class NotificationLoop(
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

    /** Asks for a tick now; ignored while the loop is stopped. */
    fun wake() {
        try {
            executor?.execute(::safeTick)
        } catch (_: RejectedExecutionException) {
            // Stopping: the next start ticks anyway.
        }
    }

    private fun daemon(task: Runnable) = Thread(task, "sard-notifications").apply { isDaemon = true }

    /** Never throws: a thrown exception would cancel every later run of the periodic task. */
    private fun safeTick() {
        runCatching(tick).onFailure { log.warn("Notification tick failed; retrying at the next one", it) }
    }
}
