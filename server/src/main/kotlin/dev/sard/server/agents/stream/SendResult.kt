// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

/**
 * What [AgentStreams.send] did with a message. Never blocks the sender: a slow agent fills only
 * its own queue. [Queued] is not "delivered": a session that closes drops its queue, and S6
 * rebuilds what the agent runs from its next Hello.
 */
sealed interface SendResult {
    data object Queued : SendResult

    /** No session for the agent (never connected, not yet Hello'd, or closed). */
    data object NotConnected : SendResult

    /** The agent's queue is full: it is not reading. The caller decides whether to retry. */
    data object QueueFull : SendResult
}
