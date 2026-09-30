// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import org.springframework.beans.factory.ObjectProvider
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.security.SecureRandom
import java.time.Clock

@Configuration(proxyBeanMethods = false)
class RunsConfiguration {
    @Bean
    fun sources(
        sessions: TenantSessions,
        clock: Clock,
    ) = Sources(sessions, clock, UuidV7(clock, SecureRandom()))

    @Bean
    fun runs(
        sessions: TenantSessions,
        clock: Clock,
        queued: ObjectProvider<StepsQueued>,
    ) = Runs(sessions, clock, UuidV7(clock, SecureRandom()), queued.getIfUnique { StepsQueued.NONE })
}
