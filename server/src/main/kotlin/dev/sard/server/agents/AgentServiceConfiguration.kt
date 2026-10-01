// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.fleet.AgentPresence
import dev.sard.server.fleet.Agents
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.registration.Registration
import dev.sard.server.runs.RunAnnouncer
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.time.Clock

@Configuration(proxyBeanMethods = false)
class AgentServiceConfiguration {
    @Bean
    fun registration(
        sessions: TenantSessions,
        clock: Clock,
    ) = Registration(sessions, clock)

    @Bean
    fun agents(
        sessions: TenantSessions,
        presence: AgentPresence,
        announcer: RunAnnouncer,
        clock: Clock,
    ) = Agents(sessions, presence, announcer, clock)

    /** A duplicate session the stream manager confirmed is marked on the agent (S8b В4). */
    @Bean
    fun duplicateSessionMarks(agents: Agents) = DuplicateSessionMarks(agents)

    /**
     * AgentService handlers block on JDBC; injected so tests and callers can choose.
     * `destroyMethod = ""`: Dispatchers.IO is process-wide and cannot be closed.
     */
    @Bean(destroyMethod = "")
    fun agentServiceDispatcher(): CoroutineDispatcher = Dispatchers.IO
}
