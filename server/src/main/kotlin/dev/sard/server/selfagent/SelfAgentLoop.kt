// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import org.springframework.context.SmartLifecycle
import java.time.Duration
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

/**
 * Runs [check] at once when the server starts and every [interval] after the previous run ended, so checks never
 * overlap. [scheduler] makes the single-thread scheduler the checks run on; a failing [check] must not escape it
 * (SelfAgentCheck catches everything).
 */
class SelfAgentLoop(
    private val interval: Duration,
    private val scheduler: () -> ScheduledExecutorService,
    private val check: () -> Unit,
) : SmartLifecycle {
    @Volatile
    private var executor: ScheduledExecutorService? = null

    override fun start() {
        executor = scheduler().also { it.scheduleWithFixedDelay(check, 0, interval.toMillis(), TimeUnit.MILLISECONDS) }
    }

    override fun stop() {
        executor?.shutdownNow()
        executor = null
    }

    override fun isRunning() = executor != null
}
