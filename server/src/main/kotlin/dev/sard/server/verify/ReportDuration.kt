// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.runs.StepOutput
import java.time.Duration

/** How long a verification took: from the first stage's start to the last stage's finish. */
object ReportDuration {
    /** Null when the stages carry no start or no finish. */
    fun millis(stages: List<StepOutput.Stage>): Long? {
        val start = stages.mapNotNull { it.startedAt }.minOrNull()
        val finish = stages.mapNotNull { it.finishedAt }.maxOrNull()
        return if (start == null || finish == null) null else Duration.between(start, finish).toMillis()
    }
}
