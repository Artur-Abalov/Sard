// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.loop.PeriodicTask
import dev.sard.server.loop.Timing
import org.slf4j.LoggerFactory
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.context.SmartLifecycle
import java.time.Duration

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
    interval: Duration,
    store: SessionStore,
) : SmartLifecycle by PeriodicTask(
        name = "sard-session-sweep",
        timing = Timing(interval),
        onFailure = { log.warn("Session sweep failed; retrying at the next one", it) },
        action = store::sweep,
    )
