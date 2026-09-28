// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import dev.sard.server.agents.AgentSessions
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import org.springframework.beans.factory.ObjectProvider
import org.springframework.beans.factory.annotation.Qualifier
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.time.Clock
import java.time.Duration

/** Handlers S6 and S7 provide as beans; until then [LoggingInbound]. */
class InboundHandlers(
    private val reconciliation: ObjectProvider<CommandReconciliation>,
    private val progress: ObjectProvider<StepProgressHandler>,
    private val results: ObjectProvider<StepResultHandler>,
    private val logs: ObjectProvider<LogChunkHandler>,
) {
    fun extensions(
        listeners: List<AgentSessionListener>,
        lastSeen: LastSeenStore,
    ) = StreamExtensions(
        reconciliation.getIfUnique { LoggingInbound },
        progress.getIfUnique { LoggingInbound },
        results.getIfUnique { LoggingInbound },
        logs.getIfUnique { LoggingInbound },
        listeners,
        lastSeen,
    )
}

@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(AgentStreamProperties::class)
class AgentStreamConfiguration {
    @Bean
    fun agentStreamSettings(
        properties: AgentStreamProperties,
        @Value("\${sard.agent.heartbeat-interval}") heartbeatInterval: Duration,
    ): AgentStreamSettings = properties.settings(heartbeatInterval)

    @Bean
    fun agentSessionRegistry(
        clock: Clock,
        settings: AgentStreamSettings,
    ) = AgentSessionRegistry(clock, settings)

    /** Handlers may block on JDBC. `destroyMethod = ""`: the shared IO dispatcher cannot be closed. */
    @Bean(destroyMethod = "")
    fun agentStreamDispatcher(): CoroutineDispatcher = Dispatchers.IO

    @Bean
    fun inboundHandlers(
        reconciliation: ObjectProvider<CommandReconciliation>,
        progress: ObjectProvider<StepProgressHandler>,
        results: ObjectProvider<StepResultHandler>,
        logs: ObjectProvider<LogChunkHandler>,
    ) = InboundHandlers(reconciliation, progress, results, logs)

    @Bean
    fun agentStreams(
        registry: AgentSessionRegistry,
        settings: AgentStreamSettings,
        clock: Clock,
        @Qualifier("agentStreamDispatcher") dispatcher: CoroutineDispatcher,
        inbound: InboundHandlers,
        listeners: ObjectProvider<AgentSessionListener>,
        sessions: AgentSessions,
    ): AgentStreams {
        val extensions = inbound.extensions(listeners.orderedStream().toList(), AgentLastSeen(sessions))
        return AgentStreams(registry, settings, clock, dispatcher, extensions)
    }

    @Bean
    fun agentStreamSweeper(
        registry: AgentSessionRegistry,
        settings: AgentStreamSettings,
    ) = AgentStreamSweeper(settings.checkInterval) { registry.sweep() }
}
