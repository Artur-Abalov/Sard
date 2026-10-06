// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.console

import jakarta.servlet.Filter
import org.slf4j.LoggerFactory
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.web.servlet.FilterRegistrationBean
import org.springframework.context.annotation.Bean
import org.springframework.core.io.ResourceLoader

private val log = LoggerFactory.getLogger(ConsoleAutoConfiguration::class.java)

/**
 * Serves the console (the Vite build, `web/dist`) from the same port as the API (S10, ADR 0040).
 * The bundle sits inside the jar under `console/` when Gradle was given `-PsardConsoleDist`;
 * `sard.console.location` points elsewhere for tests and is not an operator setting.
 * Without a bundle the filter is not registered and console paths answer 404 as before (Р3).
 */
@AutoConfiguration
class ConsoleAutoConfiguration {
    @Bean
    fun consoleBundle(
        resources: ResourceLoader,
        @Value("\${sard.console.location:classpath:/console/}") location: String,
    ): ConsoleBundle {
        val bundle = ConsoleBundle(resources.getResource(location.trimEnd('/') + "/"))
        if (!bundle.isAvailable) log.info("The console is not included in this build: console paths answer 404")
        return bundle
    }

    @Bean
    fun consoleFilterRegistration(bundle: ConsoleBundle): FilterRegistrationBean<Filter> =
        FilterRegistrationBean<Filter>(ConsoleFilter(bundle)).apply {
            urlPatterns = listOf("/*")
            isEnabled = bundle.isAvailable
        }
}
