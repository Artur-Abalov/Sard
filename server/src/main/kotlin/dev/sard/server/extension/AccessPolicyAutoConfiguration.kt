// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.context.annotation.Import

/**
 * "The administrator may do everything" unless an extension declares its own [AccessPolicy] from an
 * auto-configuration ordered `before = [AccessPolicyAutoConfiguration::class]`; two of them refuse the start.
 */
@AutoConfiguration
@Import(AccessPolicySeamConfiguration::class)
class AccessPolicyAutoConfiguration {
    @Bean
    @ConditionalOnMissingBean
    @InternalSardApi
    fun accessPolicy(): AccessPolicy = AccessPolicy { _, _ -> true }
}

/**
 * The check that only one implementation of the seam is declared. A configuration of its own, so that the static
 * bean method (a post-processor must exist before any bean) is not in the public API of the auto-configuration.
 */
@Configuration(proxyBeanMethods = false)
internal object AccessPolicySeamConfiguration {
    /** Two extensions replacing the seam refuse the start (Р22). */
    @Bean
    @JvmStatic
    fun accessPolicySeamCheck() = singleImplementationOf(AccessPolicy::class.java)
}
