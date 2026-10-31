// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import dev.sard.server.loop.PeriodicTask
import dev.sard.server.loop.Timing
import org.springframework.context.SmartLifecycle
import java.time.Duration
import java.util.concurrent.ScheduledExecutorService

/**
 * Runs [check] at once when the server starts and every [interval] after the previous run ended, so checks never
 * overlap. [scheduler] makes the single-thread scheduler the checks run on; a failing [check] must not escape it
 * (SelfAgentCheck catches everything).
 */
class SelfAgentLoop(
    interval: Duration,
    scheduler: () -> ScheduledExecutorService,
    check: () -> Unit,
) : SmartLifecycle by PeriodicTask(
        name = "sard-self-agent",
        timing = Timing(interval, initialDelay = Duration.ZERO),
        schedulers = { scheduler() },
        action = check,
    )
