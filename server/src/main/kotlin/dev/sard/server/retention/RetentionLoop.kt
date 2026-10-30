// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import dev.sard.server.loop.PeriodicTask
import dev.sard.server.loop.Timing
import org.slf4j.LoggerFactory
import org.springframework.context.SmartLifecycle
import java.time.Duration

private val log = LoggerFactory.getLogger(RetentionLoop::class.java)

/**
 * Runs [pass] every [interval], the first time one interval after the start, on one thread: passes never overlap and
 * a failing one never stops the next. Stopping interrupts a pass between two batches; a batch is one transaction, so
 * it is either whole or not at all.
 */
class RetentionLoop(
    interval: Duration,
    pass: () -> Unit,
) : SmartLifecycle by PeriodicTask(
        name = "sard-retention",
        timing = Timing(interval),
        onFailure = { log.error("Retention pass failed with {}; retrying at the next one", it.javaClass.name) },
        action = pass,
    )
