// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.results

import dev.sard.proto.agent.v1.ConnectResponse
import dev.sard.proto.agent.v1.ResultAck
import dev.sard.server.agents.stream.AgentConnections
import dev.sard.server.agents.stream.StepResultHandler
import dev.sard.server.runs.StepResults
import io.micrometer.core.instrument.Counter
import io.micrometer.core.instrument.MeterRegistry
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration

/** `sard.run.step.results{outcome}`: results by final status, or invalid, repeated, late, not_dispatched, unknown. */
class MicrometerResultMetrics(
    private val meters: MeterRegistry,
) : ResultMetrics {
    override fun recorded(outcome: String) =
        Counter
            .builder("sard.run.step.results")
            .tag("outcome", outcome)
            .register(meters)
            .increment()
}

@Configuration(proxyBeanMethods = false)
class ResultsConfiguration {
    /** Replaces S5a's logging stub (a single bean of the type wins, AgentStreamConfiguration). */
    @Bean
    fun stepResultHandler(
        results: StepResults,
        connections: AgentConnections,
        meters: MeterRegistry,
    ): StepResultHandler =
        StepResultReceiver(
            results::record,
            { agentId, commandId ->
                val ack = ResultAck.newBuilder().setCommandId(commandId)
                connections.send(agentId, ConnectResponse.newBuilder().setResultAck(ack).build())
            },
            MicrometerResultMetrics(meters),
        )
}
