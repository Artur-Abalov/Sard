// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import dev.sard.proto.agent.v1.ConnectResponse
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import java.time.Duration
import java.time.Instant
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference

/**
 * One open Connect stream. The reader coroutine owns the last-seen and skew bookkeeping; the
 * registry and the sweep only read [lastMessageAt] and call [close].
 */
class AgentStream(
    val agent: ConnectedAgent,
    val openedAt: Instant,
    queue: Int,
) {
    private val outbox = Channel<ConnectResponse>(queue)
    private val closing = AtomicReference<StreamClose?>(null)
    private val duplicateSuspected = AtomicBoolean(false)
    private val skewReported = AtomicBoolean(false)

    @Volatile
    private var job: Job? = null
    private var lastSeenWrittenAt: Instant? = null

    @Volatile
    var lastMessageAt: Instant = openedAt
        private set

    /** Set by the first [close]; null while the stream is open or when the agent ended it. */
    val closedBy: StreamClose?
        get() = closing.get()

    /** Any message from the agent. True when it answers a duplicate suspicion: this session is alive. */
    fun received(at: Instant): Boolean {
        lastMessageAt = at
        return duplicateSuspected.getAndSet(false)
    }

    /** A second stream of this agent was refused; the next message here confirms a duplicate. */
    fun suspectDuplicate() = duplicateSuspected.set(true)

    /** True, and remembered, when `last_seen_at` was last written [interval] or more ago (or never). */
    fun lastSeenDue(
        at: Instant,
        interval: Duration,
    ): Boolean {
        val last = lastSeenWrittenAt
        val due = last == null || !at.isBefore(last + interval)
        if (due) lastSeenWrittenAt = at
        return due
    }

    fun firstSkewReport(): Boolean = !skewReported.getAndSet(true)

    /** Queues a message for the agent; false when the queue is full or the stream is done. */
    fun offer(message: ConnectResponse): Boolean = outbox.trySend(message).isSuccess

    /** Ends the stream from the server side; the first reason wins. */
    fun close(reason: StreamClose) {
        if (closing.compareAndSet(null, reason)) job?.cancel()
    }

    /** The coroutine [close] cancels; a close that came earlier cancels it at once. */
    fun bind(job: Job) {
        this.job = job
        if (closing.get() != null) job.cancel()
    }

    /** Hands queued messages to [emit] one at a time until [complete] or cancellation. */
    suspend fun drain(emit: suspend (ConnectResponse) -> Unit) {
        for (message in outbox) emit(message)
    }

    /** The agent ended its side: stop after what is queued. */
    fun complete() {
        outbox.close()
    }
}
