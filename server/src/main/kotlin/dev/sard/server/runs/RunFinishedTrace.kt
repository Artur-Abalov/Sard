// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import org.slf4j.LoggerFactory

private val log = LoggerFactory.getLogger(RunFinishedTrace::class.java)

/** Counts finished runs by status: `sard.run.finished{status}` in production (FXs Д6). */
fun interface RunFinishedCounter {
    fun count(status: RunState)
}

/**
 * A trace of every [RunFinished] the publisher hands out (FXs Д6): one info line and one count per
 * run, so "the run finished once" can be checked from the log or the metric, not only from `runs`.
 */
class RunFinishedTrace(
    private val counter: RunFinishedCounter,
) : RunFinishedListener {
    override fun runFinished(event: RunFinished) {
        log.info("run {} finished: {}", event.runId, event.status.stored)
        counter.count(event.status)
    }
}
