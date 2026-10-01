// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.agents.stream.AgentSessionListener
import dev.sard.server.agents.stream.ConnectedAgent
import org.slf4j.LoggerFactory

private val log = LoggerFactory.getLogger(DuplicateSessionMarks::class.java)

/**
 * Writes `agents.duplicate_session_at` when the stream manager confirms a duplicate session (ADR 0026,
 * rule 2: a refused second stream, then a message from the holder). A failed write is logged and dropped:
 * it must not end the stream of the agent that proved alive.
 */
class DuplicateSessionMarks(
    private val agents: Agents,
) : AgentSessionListener {
    override fun duplicateDetected(agent: ConnectedAgent) {
        runCatching { agents.markDuplicate(agent.tenantId, agent.agentId) }
            .onFailure { log.warn("agent {}: duplicate session not marked: {}", agent.agentId, it.javaClass.simpleName) }
    }
}
