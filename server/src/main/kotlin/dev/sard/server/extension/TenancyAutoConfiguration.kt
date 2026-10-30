// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import dev.sard.server.extension.TenantResolver.Companion.DEFAULT_TENANT_ID
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean
import org.springframework.context.annotation.Bean

/**
 * Pins the open core to the default tenant. An enterprise starter overrides the
 * resolver by declaring `@AutoConfiguration(before = [TenancyAutoConfiguration::class])`.
 */
@AutoConfiguration
class TenancyAutoConfiguration {
    companion object {
        /** Two extensions replacing the seam refuse the start, like the other seams (Р22). */
        @Bean
        @JvmStatic
        fun tenantResolverSeamCheck() = singleImplementationOf(TenantResolver::class.java)
    }

    @Bean
    @ConditionalOnMissingBean
    fun tenantResolver() = TenantResolver { DEFAULT_TENANT_ID }
}
