// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.agents.stream.AgentConnections
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.runs.StepsQueued
import dev.sard.server.runs.VerificationRuns
import org.springframework.beans.factory.ObjectProvider
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.security.SecureRandom
import java.time.Clock

/** Restore verification on the server (V4): the queue, the daily cycle and the history. */
@Configuration(proxyBeanMethods = false)
@EnableConfigurationProperties(VerificationProperties::class)
class VerificationConfiguration {
    @Bean
    fun verificationSettings(properties: VerificationProperties) = properties.settings()

    @Bean
    fun verifierPool(connections: AgentConnections) = VerifierPool(OnlineAgents(connections::online))

    @Bean
    fun verificationHistory(clock: Clock) = VerificationHistory(UuidV7(clock, SecureRandom()))

    @Bean
    fun verificationCycles(
        clock: Clock,
        pool: VerifierPool,
    ) = VerificationCycles(UuidV7(clock, SecureRandom()), pool)

    @Bean
    fun verificationQueue(
        clock: Clock,
        settings: VerificationSettings,
        pool: VerifierPool,
        history: VerificationHistory,
    ) = VerificationQueue(settings, pool, VerificationRuns(UuidV7(clock, SecureRandom())), history)

    @Bean
    fun verificationLoop(
        sessions: TenantSessions,
        clock: Clock,
        cycles: VerificationCycles,
        queue: VerificationQueue,
        history: VerificationHistory,
        queued: ObjectProvider<StepsQueued>,
        settings: VerificationSettings,
    ) = VerificationLoop(
        sessions,
        clock,
        cycles,
        queue,
        VerificationSettlement(history),
        queued.getIfUnique { StepsQueued.NONE },
        settings,
    )

    @Bean
    fun verificationTicker(
        properties: VerificationProperties,
        loop: VerificationLoop,
    ) = VerificationTicker(properties.interval, loop::tick)
}
