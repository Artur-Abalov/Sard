// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import dev.sard.server.loop.PeriodicTask
import dev.sard.server.loop.Timing
import org.slf4j.LoggerFactory
import org.springframework.context.SmartLifecycle
import java.time.Duration

private val log = LoggerFactory.getLogger(AgentStreamSweeper::class.java)

/** Runs [check] every [interval]: the registry sweep, and in S5a phase 3 the database check. */
class AgentStreamSweeper(
    interval: Duration,
    check: () -> Unit,
) : SmartLifecycle by PeriodicTask(
        name = "sard-agent-stream-sweeper",
        timing = Timing(interval),
        onFailure = { log.warn("Agent stream check failed; retrying at the next one", it) },
        action = check,
    )
