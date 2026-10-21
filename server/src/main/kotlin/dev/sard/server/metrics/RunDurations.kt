// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import dev.sard.server.runs.RunFinished
import dev.sard.server.runs.RunFinishedListener
import dev.sard.server.runs.RunState
import io.micrometer.core.instrument.MeterRegistry
import io.micrometer.core.instrument.Timer
import java.time.Duration

/** The bounds of the buckets of `sard.run.duration`, in seconds (owner's answer OV6). */
private val BUCKET_SECONDS = longArrayOf(60, 300, 900, 1_800, 3_600, 7_200, 14_400, 28_800, 86_400)

/** The runs whose end the histogram counts: the final states. */
private val FINAL_STATES = RunState.entries.filter { !it.active }

/**
 * `sard.run.duration{kind,status}` (H1 phase 3): how long runs take, from the server's own start of the run to its
 * end, one observation per [RunFinished]. A run cancelled before it started has no start and no observation. Every
 * combination of the labels is registered at once, so a series that has not happened shows as zero. Written on the
 * event and not by the state tick, so it neither waits for a tick nor depends on the database.
 */
class RunDurations(
    meters: MeterRegistry,
) : RunFinishedListener {
    private val timers =
        MetricRunKind.entries
            .flatMap { kind -> FINAL_STATES.map { state -> (kind to state) } }
            .associateWith { (kind, state) ->
                Timer
                    .builder(SardMetricNames.RUN_DURATION)
                    .tag("kind", kind.tag)
                    .tag("status", state.stored)
                    .serviceLevelObjectives(*BUCKET_SECONDS.map(Duration::ofSeconds).toTypedArray())
                    .register(meters)
            }

    override fun runFinished(event: RunFinished) {
        val startedAt = event.startedAt ?: return
        val timer = timers.getValue(MetricRunKind.of(event.trigger, event.kind) to event.status)
        timer.record(Duration.between(startedAt, event.finishedAt))
    }
}
