// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.proto.agent.v1.ConnectResponse
import dev.sard.proto.agent.v1.RunStep
import dev.sard.server.agents.stream.AgentConnections
import dev.sard.server.agents.stream.AgentSessionRegistry
import dev.sard.server.agents.stream.AgentStreamSettings
import dev.sard.server.agents.stream.AgentStreamSweeper
import dev.sard.server.agents.stream.CommandReconciliation
import dev.sard.server.agents.stream.SendResult
import dev.sard.server.runs.StepCounts
import dev.sard.server.runs.StepTransitions
import dev.sard.server.runs.StepsQueued
import dev.sard.server.runs.WaitingSteps
import io.micrometer.core.instrument.Counter
import io.micrometer.core.instrument.Gauge
import io.micrometer.core.instrument.MeterRegistry
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.time.Clock
import java.time.Duration
import java.util.UUID
import java.util.concurrent.atomic.AtomicLong

private const val LOST_AFTER_HEARTBEATS = 2

data class DispatchSettings(
    /** How long a running step absent from Hello waits for its result before it is lost. */
    val lostAfter: Duration,
    /** How often the window is checked and queued steps of online agents are retried. */
    val checkInterval: Duration,
)

/** `sard.run.dispatch.*`; the window is counted in heartbeat intervals, like the stream's (ADR 0026). */
@ConfigurationProperties("sard.run.dispatch")
data class DispatchProperties(
    val lostAfterHeartbeats: Int = LOST_AFTER_HEARTBEATS,
    /** Unset: `sard.agent.stream.check-interval`. */
    val checkInterval: Duration? = null,
) {
    fun settings(
        heartbeatInterval: Duration,
        streamCheckInterval: Duration,
    ): DispatchSettings {
        require(lostAfterHeartbeats >= 1) { "sard.run.dispatch.lost-after-heartbeats must be at least 1" }
        val interval = checkInterval ?: streamCheckInterval
        require(interval.isPositive) { "sard.run.dispatch.check-interval must be positive" }
        return DispatchSettings(heartbeatInterval.multipliedBy(lostAfterHeartbeats.toLong()), interval)
    }
}

/** AgentLinks over the S5a session registry and connections. */
class ConnectionLinks(
    private val connections: AgentConnections,
    private val registry: AgentSessionRegistry,
) : AgentLinks {
    override fun connected() = registry.sessions().map { it.agent }

    override fun online(agentId: UUID) = connections.online(agentId)

    override fun send(
        agentId: UUID,
        step: RunStep,
    ): SendResult = connections.send(agentId, ConnectResponse.newBuilder().setRunStep(step).build())
}

/** Micrometer meters; the gauges read the counts of the last check. */
class MicrometerDispatchMetrics(
    meters: MeterRegistry,
) : DispatchMetrics {
    private val redispatched = Counter.builder("sard.run.steps.redispatched").register(meters)
    private val refused = Counter.builder("sard.run.steps.dispatch.failed").register(meters)
    private val queued = AtomicLong()
    private val dispatched = AtomicLong()

    init {
        Gauge.builder("sard.run.steps.queued", queued) { it.get().toDouble() }.register(meters)
        Gauge.builder("sard.run.steps.dispatched", dispatched) { it.get().toDouble() }.register(meters)
    }

    override fun redispatched() = redispatched.increment()

    override fun refused() = refused.increment()

    override fun waiting(steps: WaitingSteps) {
        queued.set(steps.queued)
        dispatched.set(steps.dispatched)
    }
}

@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(DispatchProperties::class)
class DispatchConfiguration {
    @Bean
    fun dispatchSettings(
        properties: DispatchProperties,
        stream: AgentStreamSettings,
    ) = properties.settings(stream.heartbeatInterval, stream.checkInterval)

    @Bean
    fun stepDispatcher(
        transitions: StepTransitions,
        connections: AgentConnections,
        registry: AgentSessionRegistry,
        clock: Clock,
        settings: DispatchSettings,
        meters: MeterRegistry,
        counts: StepCounts,
    ) = StepDispatcher(
        transitions,
        ConnectionLinks(connections, registry),
        clock,
        settings.lostAfter,
        MicrometerDispatchMetrics(meters),
        counts::waiting,
    )

    /** S5a's extension point: called once per stream, after Hello, in the agent's gRPC Context. */
    @Bean
    fun commandReconciliation(dispatcher: StepDispatcher) = CommandReconciliation(dispatcher::onHello)

    @Bean
    fun stepsQueued(dispatcher: StepDispatcher) = StepsQueued(dispatcher::onQueued)

    @Bean
    fun dispatchSweeper(
        dispatcher: StepDispatcher,
        settings: DispatchSettings,
    ) = AgentStreamSweeper(settings.checkInterval, dispatcher::tick)
}
