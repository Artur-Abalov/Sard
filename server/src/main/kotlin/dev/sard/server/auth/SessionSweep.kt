// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import org.slf4j.LoggerFactory
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.context.SmartLifecycle
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

private val log = LoggerFactory.getLogger(SessionSweep::class.java)
private const val INTERVAL_SECONDS = 60L

/** `sard.session.*`: how often the sessions that ran out are looked for; an event comes at most this late (Р23). */
@ConfigurationProperties("sard.session")
data class SessionProperties(
    val sweepInterval: Duration = Duration.ofSeconds(INTERVAL_SECONDS),
) {
    fun interval(): Duration {
        require(sweepInterval.isPositive) { "sard.session.sweep-interval must be positive" }
        return sweepInterval
    }
}

/** Runs [SessionStore.sweep] every [interval] on one thread, so a session nobody uses any more still ends visibly. */
class SessionSweep(
    private val interval: Duration,
    private val store: SessionStore,
) : SmartLifecycle {
    @Volatile
    private var executor: ScheduledExecutorService? = null

    override fun start() {
        val scheduler = Executors.newSingleThreadScheduledExecutor(::daemon)
        scheduler.scheduleWithFixedDelay(::safeSweep, interval.toMillis(), interval.toMillis(), TimeUnit.MILLISECONDS)
        executor = scheduler
    }

    override fun stop() {
        executor?.shutdownNow()
        executor = null
    }

    override fun isRunning() = executor != null

    private fun daemon(task: Runnable) = Thread(task, "sard-session-sweep").apply { isDaemon = true }

    /** Never throws: a thrown exception would cancel every later run of the periodic task. */
    private fun safeSweep() {
        runCatching(store::sweep).onFailure { log.warn("Session sweep failed; retrying at the next one", it) }
    }
}
