// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import org.slf4j.LoggerFactory
import org.springframework.context.SmartLifecycle
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

private val log = LoggerFactory.getLogger(RetentionLoop::class.java)

/**
 * Runs [pass] every [interval], the first time one interval after the start, on one thread: passes never overlap and
 * a failing one never stops the next. Stopping interrupts a pass between two batches; a batch is one transaction, so
 * it is either whole or not at all.
 */
class RetentionLoop(
    private val interval: Duration,
    private val pass: () -> Unit,
) : SmartLifecycle {
    @Volatile
    private var executor: ScheduledExecutorService? = null

    override fun start() {
        val scheduler = Executors.newSingleThreadScheduledExecutor(::daemon)
        scheduler.scheduleWithFixedDelay(::safePass, interval.toMillis(), interval.toMillis(), TimeUnit.MILLISECONDS)
        executor = scheduler
    }

    private fun daemon(task: Runnable) = Thread(task, "sard-retention").apply { isDaemon = true }

    override fun stop() {
        executor?.shutdownNow()
        executor = null
    }

    override fun isRunning() = executor != null

    /** Never throws: a thrown exception would cancel every later run of the periodic task. */
    private fun safePass() {
        runCatching(pass)
            .onFailure { log.error("Retention pass failed with {}; retrying at the next one", it.javaClass.name) }
    }
}
