// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import java.time.Clock
import java.time.Duration
import java.util.UUID

/** What a Hello got: the agent's slot (maybe from a silent predecessor) or a duplicate refusal. */
sealed interface Claim {
    data class Accepted(
        val replaced: AgentStream?,
    ) : Claim

    data object Duplicate : Claim
}

/**
 * agent_id → its one live session, in memory (stage 1: one server instance, ADR 00XX-draft
 * stream manager). A stream is a session from its Hello on; before that it is only tracked for
 * the hello timeout. One lock guards both maps; nothing under it blocks.
 */
class AgentSessionRegistry(
    private val clock: Clock,
    private val settings: AgentStreamSettings,
) {
    private val lock = Any()
    private val opened = LinkedHashSet<AgentStream>()
    private val sessions = HashMap<UUID, AgentStream>()
    private var closedWith: StreamClose? = null

    /** Tracks a new stream for the hello timeout; after [closeAll] closes it at once. */
    fun opened(stream: AgentStream) {
        val refusal = synchronized(lock) { closedWith.also { if (it == null) opened += stream } }
        refusal?.let(stream::close)
    }

    /**
     * The duplicate rule (D5): a session that got a message within the duplicate window is
     * alive, so the new stream is refused and the live one is suspected; a silent one is closed
     * as replaced — the agent reconnected before the server noticed the old connection die.
     */
    fun claim(stream: AgentStream): Claim =
        synchronized(lock) {
            opened -= stream
            val holder = sessions[stream.agent.agentId]
            if (holder != null && silentFor(holder) < settings.duplicateWindow) {
                holder.suspectDuplicate()
                return Claim.Duplicate
            }
            holder?.close(StreamCloseReason.SESSION_REPLACED.close())
            sessions[stream.agent.agentId] = stream
            Claim.Accepted(replaced = holder)
        }

    /** Forgets [stream]; true when it held its agent's slot (so the agent disconnected). */
    fun release(stream: AgentStream): Boolean =
        synchronized(lock) {
            opened -= stream
            sessions.remove(stream.agent.agentId, stream)
        }

    fun session(agentId: UUID): AgentStream? = synchronized(lock) { sessions[agentId] }

    fun sessions(): List<AgentStream> = synchronized(lock) { sessions.values.toList() }

    fun count(): Int = synchronized(lock) { sessions.size }

    fun reopen() {
        synchronized(lock) { closedWith = null }
    }

    /** Closes every stream with [close] and every stream opened until [reopen] (server stopping). */
    fun closeAll(close: StreamClose): Int {
        val all =
            synchronized(lock) {
                closedWith = close
                sessions.values + opened
            }
        all.forEach { it.close(close) }
        return all.size
    }

    /** Online: a session holds the slot and got a message within `offlineAfter`. */
    fun online(agentId: UUID): Boolean = session(agentId)?.let { silentFor(it) < settings.offlineAfter } ?: false

    /** Closes silent sessions and streams that owe a Hello; returns what it closed. */
    fun sweep(): List<AgentStream> {
        val (expired, helloless) =
            synchronized(lock) {
                sessions.values.filter { silentFor(it) >= settings.offlineAfter } to
                    opened.filter { age(it) >= settings.helloTimeout }
            }
        expired.forEach { it.close(StreamCloseReason.SESSION_EXPIRED.close()) }
        helloless.forEach { it.close(StreamCloseReason.HELLO_REQUIRED.close()) }
        return expired + helloless
    }

    private fun silentFor(stream: AgentStream): Duration = Duration.between(stream.lastMessageAt, clock.instant())

    private fun age(stream: AgentStream): Duration = Duration.between(stream.openedAt, clock.instant())
}
