// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import dev.sard.server.runs.IncompleteReason
import dev.sard.server.runs.RunFinished
import dev.sard.server.runs.RunFinishedListener
import dev.sard.server.runs.RunState
import io.micrometer.core.instrument.Counter
import io.micrometer.core.instrument.MeterRegistry

/**
 * `sard.run.partial{reason}` (PL7-2, НБ10): a backup that ended failed with a snapshot of its own, by why that
 * snapshot is incomplete. It is a subset of `sard.run.finished{status="failed"}`, so the failures without a snapshot
 * are the difference. All three reasons are registered at once, so a series that has not happened shows as zero.
 */
class RunPartials(
    meters: MeterRegistry,
) : RunFinishedListener {
    private val counters =
        IncompleteReason.entries.associateWith { reason ->
            Counter.builder(SardMetricNames.RUN_PARTIAL).tag("reason", reason.stored).register(meters)
        }

    override fun runFinished(event: RunFinished) {
        val reason = event.incompleteReason ?: return
        if (event.status == RunState.FAILED) counters.getValue(reason).increment()
    }
}
