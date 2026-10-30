// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean
import org.springframework.context.annotation.Bean

/**
 * The free edition, unless an enterprise starter declares its own [EditionProvider] from an auto-configuration
 * ordered `before = [EditionAutoConfiguration::class]` (ADR 0013, 0021).
 */
@AutoConfiguration
class EditionAutoConfiguration {
    companion object {
        /** Two extensions replacing the seam refuse the start (Р22). */
        @Bean
        @JvmStatic
        fun editionSeamCheck() = singleImplementationOf(EditionProvider::class.java)
    }

    @Bean
    @ConditionalOnMissingBean
    fun editionProvider(): EditionProvider =
        object : EditionProvider {
            override fun edition() = Edition.FREE

            override fun licence(): LicenceSummary? = null
        }
}
