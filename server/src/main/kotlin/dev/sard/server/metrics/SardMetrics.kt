// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import java.time.Duration

/** Micrometer metrics exported to Prometheus (roadmap: observability, stage 2). */
interface SardMetrics {
    fun backupFinished(
        workflowId: String,
        duration: Duration,
        succeeded: Boolean,
    )
}
