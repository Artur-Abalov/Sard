// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import io.micrometer.core.instrument.Counter
import io.micrometer.core.instrument.MeterRegistry
import org.slf4j.LoggerFactory
import java.time.Clock

private val log = LoggerFactory.getLogger(StateRefresh::class.java)

/**
 * One tick of the state metrics (H1 phase 3): [read] the state, put it in the [gauges]. A tick that fails changes no
 * value and not the time of the last refresh; it counts in `sard.metrics.state.refresh.failures` and writes one WARN
 * with the class of the exception, never its message (which may hold the SQL), so the stale state shows as the time
 * since the last refresh. It never throws.
 */
class StateRefresh(
    private val read: () -> StateSnapshot,
    private val gauges: StateGauges,
    private val clock: Clock,
    meters: MeterRegistry,
) {
    private val failures = Counter.builder(SardMetricNames.METRICS_STATE_REFRESH_FAILURES).register(meters)

    fun tick() {
        val at = clock.instant()
        runCatching { gauges.publish(read(), at) }.onFailure {
            failures.increment()
            log.warn("Metrics state not refreshed, the values of the last refresh stay: {}", it.javaClass.name)
        }
    }
}
