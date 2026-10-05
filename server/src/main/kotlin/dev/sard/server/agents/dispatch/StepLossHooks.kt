// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.server.agents.stream.AgentSessionListener
import dev.sard.server.agents.stream.ConnectedAgent
import org.springframework.context.SmartLifecycle

/** S5a's session end, for the lost deadline (FXs): every reason the stream manager reports. */
class StepLossOnDisconnect(
    private val dispatcher: StepDispatcher,
) : AgentSessionListener {
    override fun disconnected(
        agent: ConnectedAgent,
        reason: String,
    ) = dispatcher.onDisconnected(agent, reason)
}

/**
 * Gives the steps in flight a fresh window once per start, before the gRPC server takes the first
 * stream (its phase is [SmartLifecycle.DEFAULT_PHASE]): a Hello can then only clear a deadline,
 * never be overwritten by this one. A failure fails the start: the steps would have no deadline.
 */
class StepLossOnStart(
    private val start: () -> Unit,
) : SmartLifecycle {
    @Volatile private var running = false

    override fun start() {
        start.invoke()
        running = true
    }

    override fun stop() {
        running = false
    }

    override fun isRunning() = running

    override fun getPhase() = SmartLifecycle.DEFAULT_PHASE - 1
}
