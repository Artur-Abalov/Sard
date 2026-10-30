// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.loop.PeriodicTask
import dev.sard.server.loop.Timing
import org.slf4j.LoggerFactory
import org.springframework.context.SmartLifecycle
import java.time.Duration

private val log = LoggerFactory.getLogger(NotificationLoop::class.java)

/**
 * Runs [tick] every [interval] on one thread, and soon after [wake] (a run just finished). Ticks
 * never overlap: the scheduler has a single thread.
 */
class NotificationLoop private constructor(
    private val task: PeriodicTask,
) : SmartLifecycle by task {
    constructor(interval: Duration, tick: () -> Unit) : this(
        PeriodicTask(
            name = "sard-notifications",
            timing = Timing(interval),
            onFailure = { log.warn("Notification tick failed; retrying at the next one", it) },
            action = tick,
        ),
    )

    /** Asks for a tick now; ignored while the loop is stopped. */
    fun wake() = task.runNow()
}
