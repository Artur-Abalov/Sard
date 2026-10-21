// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import io.micrometer.core.instrument.Counter
import io.micrometer.core.instrument.MeterRegistry

/**
 * What the cleaning deletes, in the order the pass works and the summary line lists it. [tag] is the value of the
 * `kind` tag of the counter and the name in the summary.
 */
enum class RetentionKind(
    val tag: String,
) {
    STEP_LOG_PARTITION("step_log_partition"),
    NOTIFICATION_DELIVERY("notification_delivery"),
    SCHEDULE_FIRE("schedule_fire"),
    RESTORE_VERIFICATION("restore_verification"),
    VERIFICATION_JOB("verification_job"),
    RUN_STEP("run_step"),
    RUN("run"),
}

/** Told after each committed batch how many rows of a [RetentionKind] it deleted. */
fun interface RetentionMetrics {
    fun deleted(
        kind: RetentionKind,
        count: Int,
    )
}

/** The counter of deleted rows, one series per [RetentionKind]; its name is written here and nowhere else. */
class MicrometerRetentionMetrics(
    meters: MeterRegistry,
) : RetentionMetrics {
    private val counters =
        RetentionKind.entries.associateWith {
            Counter.builder("sard.retention.deleted").tag("kind", it.tag).register(meters)
        }

    override fun deleted(
        kind: RetentionKind,
        count: Int,
    ) = counters.getValue(kind).increment(count.toDouble())
}
