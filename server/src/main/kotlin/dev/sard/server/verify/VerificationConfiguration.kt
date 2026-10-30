// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.agents.stream.AgentConnections
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.runs.CancelMarks
import dev.sard.server.runs.RunEvents
import dev.sard.server.runs.Runs
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
        events: RunEvents,
    ) = VerificationQueue(settings, pool, VerificationRuns(UuidV7(clock, SecureRandom()), events), history)

    @Bean
    fun verificationLoop(
        sessions: TenantSessions,
        clock: Clock,
        cycles: VerificationCycles,
        queue: VerificationQueue,
        settlement: VerificationSettlement,
        queued: ObjectProvider<StepsQueued>,
        settings: VerificationSettings,
        pool: VerifierPool,
    ) = VerificationLoop(
        sessions,
        clock,
        cycles,
        queue,
        settlement,
        queued.getIfUnique { StepsQueued.NONE },
        VerificationSchedule(settings),
        StaleEpisodes(UuidV7(clock, SecureRandom())),
        NoVerifierEpisodes(UuidV7(clock, SecureRandom()), pool),
    )

    @Bean
    fun verifications(
        sessions: TenantSessions,
        clock: Clock,
        cycles: VerificationCycles,
        pool: VerifierPool,
        settings: VerificationSettings,
    ) = Verifications(sessions, clock, cycles, pool, VerificationSchedule(settings))

    @Bean
    fun verificationReads(
        sessions: TenantSessions,
        clock: Clock,
        pool: VerifierPool,
        settings: VerificationSettings,
    ) = VerificationReads(sessions, clock, pool, settings)

    @Bean
    fun verificationReports(
        sessions: TenantSessions,
        clock: Clock,
        settings: VerificationSettings,
    ) = VerificationReports(sessions, clock, VerificationSchedule(settings))

    @Bean
    fun verificationTicker(
        properties: VerificationProperties,
        loop: VerificationLoop,
    ) = VerificationTicker(properties.interval, loop::tick)
}

/** The end of a job's attempt and its cancel by an administrator (J1f). */
@Configuration(proxyBeanMethods = false)
class VerificationCancelConfiguration {
    @Bean
    fun verificationSettlement(history: VerificationHistory) = VerificationSettlement(history)

    @Bean
    fun verificationCancels(
        sessions: TenantSessions,
        clock: Clock,
        runs: Runs,
        marks: CancelMarks,
        settlement: VerificationSettlement,
        history: VerificationHistory,
    ) = VerificationCancels(sessions, clock, runs, marks, settlement, history)
}
