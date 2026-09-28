// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import io.micrometer.core.instrument.Counter
import io.micrometer.core.instrument.DistributionSummary
import io.micrometer.core.instrument.Gauge
import io.micrometer.core.instrument.MeterRegistry
import java.time.Duration

private const val NANOS_PER_SECOND = 1e9

/**
 * `sard.agents.connected` (sessions in the registry), `sard.agent.duplicate.sessions` and
 * `sard.agent.clock.skew` (absolute, seconds; the sign is in the log). Micrometer from actuator;
 * no exporter yet (stage 2).
 */
class MicrometerStreamMetrics(
    meters: MeterRegistry,
    registry: AgentSessionRegistry,
) : StreamMetrics {
    private val duplicates = Counter.builder("sard.agent.duplicate.sessions").register(meters)
    private val skew = DistributionSummary.builder("sard.agent.clock.skew").baseUnit("seconds").register(meters)

    init {
        Gauge.builder("sard.agents.connected", registry) { it.count().toDouble() }.register(meters)
    }

    override fun duplicateDetected() = duplicates.increment()

    override fun clockSkew(skew: Duration) = this.skew.record(skew.abs().toNanos() / NANOS_PER_SECOND)
}
