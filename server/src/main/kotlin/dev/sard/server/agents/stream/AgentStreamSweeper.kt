// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import org.slf4j.LoggerFactory
import org.springframework.context.SmartLifecycle
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

private val log = LoggerFactory.getLogger(AgentStreamSweeper::class.java)

/** Runs [check] every [interval]: the registry sweep, and in S5a phase 3 the database check. */
class AgentStreamSweeper(
    private val interval: Duration,
    private val check: () -> Unit,
) : SmartLifecycle {
    private var executor: ScheduledExecutorService? = null

    override fun start() {
        val scheduler = Executors.newSingleThreadScheduledExecutor(::daemon)
        scheduler.scheduleWithFixedDelay(::tick, interval.toMillis(), interval.toMillis(), TimeUnit.MILLISECONDS)
        executor = scheduler
    }

    override fun stop() {
        executor?.shutdownNow()
        executor = null
    }

    override fun isRunning() = executor != null

    private fun daemon(task: Runnable) = Thread(task, "sard-agent-stream-sweeper").apply { isDaemon = true }

    /** Never throws: a thrown exception would cancel every later run of the periodic task. */
    private fun tick() {
        runCatching(check).onFailure { log.warn("Agent stream check failed; retrying at the next one", it) }
    }
}
