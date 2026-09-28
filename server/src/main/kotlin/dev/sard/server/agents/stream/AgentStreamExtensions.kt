// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import dev.sard.proto.agent.v1.LogChunk
import dev.sard.proto.agent.v1.StepProgress
import dev.sard.proto.agent.v1.StepResult
import dev.sard.server.agents.AgentPrincipal
import org.slf4j.LoggerFactory
import java.time.Instant
import java.util.UUID

/** The agent at the other end of a stream, as its certificate established it (S3). */
data class ConnectedAgent(
    val agentId: UUID,
    val tenantId: UUID,
    val serial: String,
) {
    companion object {
        fun of(principal: AgentPrincipal) = ConnectedAgent(principal.agentId, principal.tenantId, principal.serial)
    }
}

// Extension points. Each is called on the stream's own coroutine, in message order, on the
// stream dispatcher, inside the agent's gRPC Context (so `AgentSessions.inTenant` works). A
// handler that throws ends the stream; a slow one delays only this agent.

/** Reconciles commands after Hello and delivers pending ones (S6); called once the slot is taken. */
fun interface CommandReconciliation {
    fun onHello(
        agent: ConnectedAgent,
        runningCommandIds: List<String>,
    )
}

/** Intermediate state of a running command (S6). */
fun interface StepProgressHandler {
    fun handle(
        agent: ConnectedAgent,
        progress: StepProgress,
    )
}

/** Final outcome of a command; the ResultAck is the handler's job (S7). */
fun interface StepResultHandler {
    fun handle(
        agent: ConnectedAgent,
        result: StepResult,
    )
}

/** Log output of a running command (S7). */
fun interface LogChunkHandler {
    fun handle(
        agent: ConnectedAgent,
        chunk: LogChunk,
    )
}

/** Session lifecycle for other components; every method defaults to doing nothing. */
interface AgentSessionListener {
    /** The agent's Hello took its slot: it is online. */
    fun connected(agent: ConnectedAgent) {}

    /** The agent's session ended; [reason] is a [StreamCloseReason] name, an auth failure or STREAM_ENDED. */
    fun disconnected(
        agent: ConnectedAgent,
        reason: String,
    ) {}

    /** A stream was refused as a duplicate and the session holding the slot then proved alive (D5). */
    fun duplicateDetected(agent: ConnectedAgent) {}
}

/** Where `agents.last_seen_at` is written, at most once per heartbeat interval per stream. */
fun interface LastSeenStore {
    fun record(
        agent: ConnectedAgent,
        at: Instant,
    )
}

/** What the stream manager calls out to; S6 and S7 replace the defaults with beans. */
data class StreamExtensions(
    val reconciliation: CommandReconciliation,
    val progress: StepProgressHandler,
    val results: StepResultHandler,
    val logs: LogChunkHandler,
    val listeners: List<AgentSessionListener>,
    val lastSeen: LastSeenStore,
)

/** The defaults until S6 and S7: a debug line with ids only, never message content. */
object LoggingInbound : CommandReconciliation, StepProgressHandler, StepResultHandler, LogChunkHandler {
    private val log = LoggerFactory.getLogger(LoggingInbound::class.java)

    override fun onHello(
        agent: ConnectedAgent,
        runningCommandIds: List<String>,
    ) = log.debug("agent {} hello: {} running commands", agent.agentId, runningCommandIds.size)

    override fun handle(
        agent: ConnectedAgent,
        progress: StepProgress,
    ) = log.debug("agent {} progress of command {}: {}", agent.agentId, progress.commandId, progress.phase)

    override fun handle(
        agent: ConnectedAgent,
        result: StepResult,
    ) = log.debug("agent {} result of command {}: {}", agent.agentId, result.commandId, result.status)

    override fun handle(
        agent: ConnectedAgent,
        chunk: LogChunk,
    ) = log.debug("agent {} log of command {}: {} lines", agent.agentId, chunk.commandId, chunk.linesCount)
}
