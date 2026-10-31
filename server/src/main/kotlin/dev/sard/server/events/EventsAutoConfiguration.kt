// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.events

import dev.sard.server.extension.SubjectSource
import dev.sard.server.extension.TenantResolver
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean
import org.springframework.context.ApplicationEventPublisher
import org.springframework.context.annotation.Bean
import java.time.Clock

/** The events of the core (CORE1 Р5): the multicaster that isolates listeners, and the publisher the core uses. */
@AutoConfiguration(afterName = ["dev.sard.server.auth.AdminAuthSupportAutoConfiguration"])
class EventsAutoConfiguration {
    @Bean(name = ["applicationEventMulticaster"])
    @ConditionalOnMissingBean(name = ["applicationEventMulticaster"])
    fun applicationEventMulticaster() = SardEventMulticaster()

    @Bean
    @ConditionalOnMissingBean
    fun sardEventPublisher(publisher: ApplicationEventPublisher) = SardEventPublisher(publisher)

    @Bean
    @ConditionalOnMissingBean
    fun subjectActors(subjects: SubjectSource): SubjectActors = SubjectActors(subjects)

    @Bean
    @ConditionalOnMissingBean
    fun settingsEvents(
        publisher: SardEventPublisher,
        actors: SubjectActors,
        clock: Clock,
        tenants: TenantResolver,
    ) = SettingsEvents(publisher, actors, clock, tenants)
}
