// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.micrometer.core.instrument.simple.SimpleMeterRegistry
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals

/** F3a metrics: fires by kind and outcome, and how far the scheduler lags behind its oldest due fire. */
@MutFlowTest
class MicrometerSchedulerMetricsTest {
    private val meters = SimpleMeterRegistry()

    private fun fires(
        kind: String,
        outcome: String,
    ) = meters
        .get("sard.scheduler.fires")
        .tag("kind", kind)
        .tag("outcome", outcome)
        .counter()
        .count()

    @Test
    fun `fires are counted by kind and outcome`() {
        val metrics = MicrometerSchedulerMetrics(meters)
        MutFlow.underTest {
            metrics.fired(FireKind.SCHEDULE, FireOutcome.RUN_CREATED)
            metrics.fired(FireKind.SCHEDULE, FireOutcome.RUN_CREATED)
            metrics.fired(FireKind.CATCH_UP, FireOutcome.SKIPPED_ACTIVE)
        }
        assertEquals(2.0, fires("schedule", "run_created"))
        assertEquals(1.0, fires("catch_up", "skipped_active"))
    }

    @Test
    fun `the lag gauge shows the last tick's lag in seconds`() {
        val metrics = MicrometerSchedulerMetrics(meters)
        val gauge = meters.get("sard.scheduler.lag.seconds").gauge()
        assertEquals(0.0, gauge.value())
        MutFlow.underTest { metrics.lag(Duration.ofMillis(1500)) }
        assertEquals(1.5, gauge.value())
        MutFlow.underTest { metrics.lag(Duration.ZERO) }
        assertEquals(0.0, gauge.value())
    }
}
