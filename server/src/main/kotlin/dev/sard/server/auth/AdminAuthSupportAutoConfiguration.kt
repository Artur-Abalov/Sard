// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.SESSION_REQUEST_ATTRIBUTE
import dev.sard.server.events.ActorSource
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.web.context.request.RequestContextHolder
import org.springframework.web.context.request.ServletRequestAttributes

/** What the events of the core need from the session: who is acting, and the sweep that ends forgotten sessions. */
@EnableConfigurationProperties(SessionProperties::class)
@AutoConfiguration(after = [AdminAuthAutoConfiguration::class])
class AdminAuthSupportAutoConfiguration {
    /** The subject of the session of the request on this thread; none on the server's own threads (Р18). */
    @Bean
    @ConditionalOnMissingBean
    fun actorSource(): ActorSource =
        ActorSource {
            val request = (RequestContextHolder.getRequestAttributes() as? ServletRequestAttributes)?.request
            (request?.getAttribute(SESSION_REQUEST_ATTRIBUTE) as? AdminSession)?.subject
        }

    @Bean
    @ConditionalOnMissingBean
    fun sessionSweep(
        properties: SessionProperties,
        sessionStore: SessionStore,
    ) = SessionSweep(properties.interval(), sessionStore)
}
