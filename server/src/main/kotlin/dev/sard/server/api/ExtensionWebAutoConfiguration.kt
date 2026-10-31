// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.InternalSardApi
import org.springframework.beans.factory.SmartInitializingSingleton
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.condition.ConditionalOnWebApplication
import org.springframework.context.annotation.Bean
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping

/**
 * The REST of an extension lives under /api/v1, where the session of the core guards it (CORE1 Р8). A controller
 * of an extension anywhere else is open to the world, so it refuses the start; the text names its class and path.
 * The controllers of the core itself and of the frameworks are not the extension's and not checked
 * ([ExtensionControllers]).
 */
@AutoConfiguration
@ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
class ExtensionWebAutoConfiguration {
    @Bean
    @InternalSardApi
    fun extensionControllersUnderApi(mappings: List<RequestMappingHandlerMapping>) =
        SmartInitializingSingleton {
            val core =
                ExtensionWebAutoConfiguration::class.java.protectionDomain
                    ?.codeSource
                    ?.location
            val handlers = mappings.flatMap { mapping -> mapping.handlerMethods.map { it.key to it.value } }
            val outside = ExtensionControllers(core, ExtensionControllers.frameworkLocations()).outsideApi(handlers)
            check(outside.isEmpty()) {
                "An extension controller outside /api/v1/ would bypass the session of the core: ${outside.sorted()}"
            }
        }
}
