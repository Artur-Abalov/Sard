// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import dev.sard.server.extension.AccessPolicy
import dev.sard.server.extension.AccessPolicyAutoConfiguration
import dev.sard.server.extension.EditionAutoConfiguration
import dev.sard.server.extension.EditionProvider
import dev.sard.server.extension.SardExtension
import dev.sard.server.extension.SardSecurityCustomizer
import dev.sard.server.extension.SubjectSource
import dev.sard.server.extension.SubjectSourceAutoConfiguration
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.security.config.annotation.web.builders.HttpSecurity
import org.springframework.security.web.SecurityFilterChain

/**
 * The sample starter: an extension, the edition and licence seam and the access policy replaced, a listener of the
 * events, its own endpoints, its entry into the security chain of the core and, in db/migration-extensions, its own
 * migration. Ordered before the core's defaults,
 * as ADR 0013 and 0021 ask of a starter that replaces one.
 */
@AutoConfiguration(
    before = [EditionAutoConfiguration::class, AccessPolicyAutoConfiguration::class, SubjectSourceAutoConfiguration::class],
)
@EnableConfigurationProperties(SampleProperties::class)
class SampleAutoConfiguration {
    @Bean
    fun sampleExtension(): SardExtension =
        object : SardExtension {
            override val id = "sample"
            override val displayName = "Sard Sample Extension"
        }

    @Bean("sample-edition")
    fun sampleEdition(properties: SampleProperties): EditionProvider = SampleEdition(properties.licence)

    @Bean("sample-policy")
    fun samplePolicy(properties: SampleProperties): AccessPolicy =
        SamplePolicy(properties.deny.map { it.trim() }.toSet(), properties.policyFailing)

    /** Replaces the subject source of the core only when `sard.sample.subject` is set. */
    @Bean("sample-subject")
    @ConditionalOnProperty("sard.sample.subject")
    fun sampleSubject(properties: SampleProperties): SubjectSource =
        SampleSubjectSource(SampleSubjectSource.parse(checkNotNull(properties.subject)))

    /** The sample's way into the security chain of the core (Р41); named after the extension's id. */
    @Bean("sample")
    fun sampleSecurity(properties: SampleProperties): SardSecurityCustomizer = SampleSecurityCustomizer(properties)

    /** Only with `sard.sample.own-chain`: a chain of its own, which the core refuses at start (Р44). */
    @Bean("sample-chain")
    @ConditionalOnProperty("sard.sample.own-chain")
    fun sampleChain(http: HttpSecurity): SecurityFilterChain = http.build()

    @Bean
    fun sampleEventListener(
        jdbc: JdbcTemplate,
        properties: SampleProperties,
    ) = SampleEventListener(jdbc, properties.listener)

    @Bean
    fun sampleController(jdbc: JdbcTemplate) = SampleController(jdbc)
}
