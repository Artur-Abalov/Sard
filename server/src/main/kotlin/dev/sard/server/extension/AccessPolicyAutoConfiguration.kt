// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean
import org.springframework.context.annotation.Bean

/**
 * "The administrator may do everything" unless an extension declares its own [AccessPolicy] from an
 * auto-configuration ordered `before = [AccessPolicyAutoConfiguration::class]`; two of them refuse the start.
 */
@AutoConfiguration
class AccessPolicyAutoConfiguration {
    companion object {
        @Bean
        @JvmStatic
        @InternalSardApi
        fun accessPolicySeamCheck() = singleImplementationOf(AccessPolicy::class.java)
    }

    @Bean
    @ConditionalOnMissingBean
    @InternalSardApi
    fun accessPolicy(): AccessPolicy = AccessPolicy { _, _ -> true }
}
