// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import com.google.protobuf.Timestamp
import dev.sard.proto.agent.v1.ConnectRequest
import dev.sard.proto.agent.v1.ConnectRequest.MessageCase
import dev.sard.proto.agent.v1.ConnectResponse
import dev.sard.proto.agent.v1.Heartbeat
import dev.sard.server.agents.AgentPrincipal
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.FlowCollector
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.job
import kotlinx.coroutines.launch
import org.slf4j.LoggerFactory
import java.time.Clock
import java.time.Duration
import java.time.Instant

/** The disconnect reason when the agent, not the server, ended the stream. */
const val STREAM_ENDED = "STREAM_ENDED"

private val log = LoggerFactory.getLogger(AgentStreams::class.java)

/**
 * AgentService.Connect (S5a): Hello first, then heartbeats, progress, logs and results up and
 * queued messages down. Each stream runs two coroutines: the reader, on [dispatcher], handles
 * incoming messages in order; the response flow is the stream's only writer and emits from the
 * outbox as fast as gRPC reports the stream ready.
 */
class AgentStreams(
    private val registry: AgentSessionRegistry,
    private val settings: AgentStreamSettings,
    private val clock: Clock,
    private val dispatcher: CoroutineDispatcher,
    private val extensions: StreamExtensions,
) {
    /** Called inside the agent's gRPC Context, where S3's interceptor put its principal. */
    fun connect(requests: Flow<ConnectRequest>): Flow<ConnectResponse> =
        flow {
            val principal = checkNotNull(AgentPrincipal.KEY.get()) { "Connect outside an authenticated agent call" }
            val stream = AgentStream(ConnectedAgent.of(principal), clock.instant(), settings.sendQueue)
            try {
                serve(stream, requests)
            } finally {
                ended(stream)
            }
        }

    private suspend fun FlowCollector<ConnectResponse>.serve(
        stream: AgentStream,
        requests: Flow<ConnectRequest>,
    ) {
        try {
            coroutineScope {
                stream.bind(coroutineContext.job)
                registry.opened(stream)
                launch(dispatcher) {
                    read(stream, requests)
                    stream.complete()
                }
                stream.drain { emit(it) }
            }
        } catch (cancelled: CancellationException) {
            throw stream.closedBy?.status() ?: cancelled
        }
    }

    private suspend fun read(
        stream: AgentStream,
        requests: Flow<ConnectRequest>,
    ) {
        var greeted = false
        requests.collect { message ->
            if (greeted) receive(stream, message) else greet(stream, message)
            greeted = true
        }
    }

    private fun greet(
        stream: AgentStream,
        message: ConnectRequest,
    ) {
        if (!message.hasHello()) throw StreamCloseReason.HELLO_REQUIRED.close().status()
        if (registry.claim(stream) == Claim.Duplicate) {
            log.warn("agent {}: second stream refused, its session is alive", stream.agent.agentId)
            throw StreamCloseReason.AGENT_DUPLICATE_SESSION.close().status()
        }
        extensions.listeners.forEach { it.connected(stream.agent) }
        touch(stream)
        extensions.reconciliation.onHello(stream.agent, message.hello.runningCommandIdsList)
    }

    private fun receive(
        stream: AgentStream,
        message: ConnectRequest,
    ) {
        touch(stream)
        val agent = stream.agent
        when (message.messageCase) {
            MessageCase.HEARTBEAT -> {
                heartbeat(stream, message.heartbeat)
            }

            MessageCase.STEP_PROGRESS -> {
                extensions.progress.handle(agent, message.stepProgress)
            }

            MessageCase.STEP_RESULT -> {
                extensions.results.handle(agent, message.stepResult)
            }

            MessageCase.LOG_CHUNK -> {
                extensions.logs.handle(agent, message.logChunk)
            }

            MessageCase.HELLO, MessageCase.MESSAGE_NOT_SET -> {
                log.debug("agent {}: {} ignored", agent.agentId, message.messageCase)
            }
        }
    }

    /** Any message: liveness, the duplicate check, and `last_seen_at` at most once per interval. */
    private fun touch(stream: AgentStream) {
        val now = clock.instant()
        if (stream.received(now)) {
            extensions.metrics.duplicateDetected()
            val agent = stream.agent
            log.warn("agent {}: duplicate session, two hosts present certificate {}", agent.agentId, agent.serial)
            extensions.listeners.forEach { it.duplicateDetected(stream.agent) }
        }
        if (stream.lastSeenDue(now, settings.heartbeatInterval)) extensions.lastSeen.record(stream.agent, now)
    }

    private fun heartbeat(
        stream: AgentStream,
        heartbeat: Heartbeat,
    ) {
        if (!heartbeat.hasSentAt()) return
        val skew = Duration.between(clock.instant(), heartbeat.sentAt.toInstant())
        extensions.metrics.clockSkew(skew)
        if (skew.abs() > settings.clockSkewThreshold && stream.firstSkewReport()) {
            log.warn("agent {}: clock skew {} exceeds {}", stream.agent.agentId, skew, settings.clockSkewThreshold)
        }
    }

    private fun ended(stream: AgentStream) {
        if (!registry.release(stream)) return
        val reason = stream.closedBy?.reason ?: STREAM_ENDED
        extensions.listeners.forEach { it.disconnected(stream.agent, reason) }
    }
}

private fun Timestamp.toInstant(): Instant = Instant.ofEpochSecond(seconds, nanos.toLong())
