// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import java.time.Instant

/** Schedules and job queue (roadmap: schedules, stage 1). */
interface Scheduler {
    /** Queues a run of the workflow at [dueAt]. */
    fun enqueue(
        workflowId: String,
        dueAt: Instant,
    )
}
