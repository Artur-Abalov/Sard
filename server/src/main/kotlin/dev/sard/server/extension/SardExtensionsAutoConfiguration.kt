// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import org.slf4j.LoggerFactory
import org.springframework.beans.factory.ObjectProvider
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.context.annotation.Bean

private val log = LoggerFactory.getLogger(SardExtensionsAutoConfiguration::class.java)

/**
 * Collects every [SardExtension] bean contributed by enterprise starters.
 * Runs after all other auto-configurations so that their beans are visible.
 */
@AutoConfiguration
class SardExtensionsAutoConfiguration {
    @Bean
    fun sardExtensionRegistry(extensions: ObjectProvider<SardExtension>): ExtensionRegistry {
        val registry = ExtensionRegistry(extensions.orderedStream().toList())
        log.info("Sard extensions loaded: {}", registry.ids().ifEmpty { "none" })
        return registry
    }
}
