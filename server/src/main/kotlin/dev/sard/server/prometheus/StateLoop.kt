// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.prometheus

import dev.sard.server.loop.PeriodicTask
import dev.sard.server.loop.StopWait
import dev.sard.server.loop.Timing
import org.slf4j.LoggerFactory
import org.springframework.context.SmartLifecycle
import java.time.Duration

private const val STOP_PATIENCE_SECONDS = 10L

private val log = LoggerFactory.getLogger(StateLoop::class.java)

/**
 * Runs [tick] at the start and then [interval] after the end of each tick, on one thread of its own: ticks never
 * overlap, a slow one delays the next and nothing else, and a scrape never waits for either (МР6). The first tick is
 * in the background, so a database that is slow to answer does not delay the readiness of the server.
 */
class StateLoop(
    interval: Duration,
    stopPatience: Duration = Duration.ofSeconds(STOP_PATIENCE_SECONDS),
    tick: () -> Unit,
) : SmartLifecycle by PeriodicTask(
        name = "sard-metrics-state",
        timing = Timing(interval, initialDelay = Duration.ZERO),
        stopWait =
            StopWait(stopPatience) {
                log.warn("The state tick was still running {} s after stop", stopPatience.toSeconds())
            },
        action = tick,
    )
