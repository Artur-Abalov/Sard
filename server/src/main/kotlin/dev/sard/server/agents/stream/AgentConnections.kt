// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import dev.sard.proto.agent.v1.ConnectResponse
import dev.sard.server.agents.AgentAuthFailure
import org.slf4j.LoggerFactory
import java.util.UUID

private val log = LoggerFactory.getLogger(AgentConnections::class.java)

/**
 * The agents' open sessions as other components see them (S6, S7, later S8b): send, online,
 * close; and their upkeep — the periodic check and the server's stop and start.
 */
class AgentConnections(
    private val registry: AgentSessionRegistry,
    private val revalidation: SessionRevalidation,
) {
    /** Queues [message] for [agentId]'s session; never waits (see [SendResult]). */
    fun send(
        agentId: UUID,
        message: ConnectResponse,
    ): SendResult = registry.session(agentId)?.offer(message) ?: SendResult.NotConnected

    fun online(agentId: UUID): Boolean = registry.online(agentId)

    /** Every agent that is online now. */
    fun onlineIds(): Set<UUID> = registry.onlineIds()

    /** Ends [agentId]'s session as refused with [failure], e.g. when it is revoked; false if none. */
    fun close(
        agentId: UUID,
        failure: AgentAuthFailure,
    ): Boolean {
        val session = registry.session(agentId) ?: return false
        session.close(failure.close())
        return true
    }

    /**
     * The periodic check: sessions re-judged against the database first, so a revoked agent is
     * closed as revoked even when it is also silent; then the expiry and hello-timeout sweep.
     */
    fun check() {
        val sessions = registry.sessions()
        val failures = revalidation.failures(sessions.map { it.agent })
        for (session in sessions) {
            val failure = failures[session.agent] ?: continue
            log.info("agent {}: session closed, {}", session.agent.agentId, failure)
            session.close(failure.close())
        }
        registry.sweep()
    }

    /** The server is stopping: every stream ends with UNAVAILABLE so agents reconnect elsewhere. */
    fun shutdown() {
        val closed = registry.closeAll(StreamCloseReason.SERVER_SHUTTING_DOWN.close())
        log.info("server stopping: {} agent streams closed", closed)
    }

    /** The server (re)started: streams are accepted again (a stopped server may start again). */
    fun reopen() = registry.reopen()
}
