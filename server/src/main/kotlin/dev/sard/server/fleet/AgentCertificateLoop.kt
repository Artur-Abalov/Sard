// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.loop.PeriodicTask
import dev.sard.server.loop.Timing
import org.slf4j.LoggerFactory
import org.springframework.context.SmartLifecycle
import java.time.Duration

private val log = LoggerFactory.getLogger(AgentCertificateLoop::class.java)

/**
 * Runs [tick] every [interval] on one thread; ticks never overlap (C1 phase 3, П35). A tick that throws logs one
 * warning and the next one repeats it: the exception never stops the loop.
 */
class AgentCertificateLoop(
    interval: Duration,
    tick: () -> Unit,
) : SmartLifecycle by PeriodicTask(
        name = "sard-agent-certificates",
        timing = Timing(interval),
        onFailure = { log.warn("Agent certificate check failed; retrying at the next one", it) },
        action = tick,
    )
