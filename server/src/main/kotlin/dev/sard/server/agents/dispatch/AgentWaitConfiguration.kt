// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.server.agents.stream.AgentConnections
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.runs.AgentPresence
import dev.sard.server.runs.AgentWaits
import dev.sard.server.runs.RunAnnouncer
import dev.sard.server.runs.StepDeadlines
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.time.Clock

/** The wait of runs for offline agents (J1c): who is online, and the ticks that close the waits that are over. */
@Configuration(proxyBeanMethods = false)
class AgentWaitConfiguration {
    @Bean
    fun agentPresence(connections: AgentConnections) = AgentPresence(connections::online)

    /** The dispatcher's tick and the scheduler's call it; the server's start is the moment of this bean (ДJ9). */
    @Bean
    fun agentWaits(
        deadlines: StepDeadlines,
        sessions: TenantSessions,
        clock: Clock,
        announcer: RunAnnouncer,
        presence: AgentPresence,
        settings: DispatchSettings,
    ) = AgentWaits(deadlines, sessions, clock, announcer, presence, clock.instant(), settings.reconnectGrace)
}
