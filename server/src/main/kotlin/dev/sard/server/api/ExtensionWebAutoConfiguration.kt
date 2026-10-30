// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import org.springframework.beans.factory.SmartInitializingSingleton
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnWebApplication
import org.springframework.context.annotation.Bean
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping

private const val CORE_PACKAGE = "dev.sard.server."
private const val API_PREFIX = "/api/v1/"
private val FRAMEWORK_PACKAGES = listOf("org.springframework.", "org.springdoc.", "org.apache.", "io.swagger.")

/**
 * The REST of an extension lives under /api/v1, where the session of the core guards it (CORE1 Р8). A controller
 * of an extension anywhere else is open to the world, so it refuses the start; the text names its class and path.
 * The controllers of the core and of the frameworks are not the extension's and not checked.
 */
@AutoConfiguration
@ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
class ExtensionWebAutoConfiguration {
    @Bean
    fun extensionControllersUnderApi(mappings: List<RequestMappingHandlerMapping>) =
        SmartInitializingSingleton {
            val outside =
                mappings
                    .flatMap { it.handlerMethods.entries }
                    .filter { (_, handler) -> isExtensionClass(handler.beanType.name) }
                    .flatMap { (info, handler) ->
                        info.pathPatternsCondition
                            ?.patternValues
                            .orEmpty()
                            .map { "$it (${handler.beanType.name})" }
                    }.filterNot { it.startsWith(API_PREFIX) }
            check(outside.isEmpty()) {
                "An extension controller outside $API_PREFIX would bypass the session of the core: ${outside.sorted()}"
            }
        }

    private fun isExtensionClass(name: String): Boolean {
        val framework = FRAMEWORK_PACKAGES.any { name.startsWith(it) }
        return !framework && !name.startsWith(CORE_PACKAGE)
    }
}
