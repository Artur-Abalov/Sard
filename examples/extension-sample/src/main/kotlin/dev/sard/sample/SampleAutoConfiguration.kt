// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import dev.sard.server.extension.AccessPolicy
import dev.sard.server.extension.AccessPolicyAutoConfiguration
import dev.sard.server.extension.EditionAutoConfiguration
import dev.sard.server.extension.EditionProvider
import dev.sard.server.extension.SardExtension
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.jdbc.core.JdbcTemplate

/**
 * The sample starter: an extension, the edition and licence seam and the access policy replaced, a listener of the
 * events, its own endpoints and, in db/migration-extensions, its own migration. Ordered before the core's defaults,
 * as ADR 0013 and 0021 ask of a starter that replaces one.
 */
@AutoConfiguration(before = [EditionAutoConfiguration::class, AccessPolicyAutoConfiguration::class])
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

    @Bean
    fun sampleEventListener(
        jdbc: JdbcTemplate,
        properties: SampleProperties,
    ) = SampleEventListener(jdbc, properties.listener)

    @Bean
    fun sampleController(jdbc: JdbcTemplate) = SampleController(jdbc)
}
