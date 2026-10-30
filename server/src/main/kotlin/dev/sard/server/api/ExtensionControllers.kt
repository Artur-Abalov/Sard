// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import org.springframework.web.method.HandlerMethod
import org.springframework.web.servlet.mvc.method.RequestMappingInfo
import java.net.URL

private const val API_PREFIX = "/api/v1/"
private const val NO_PATH = "<no path>"
private val FRAMEWORK_PACKAGES = listOf("org.springframework.", "org.springdoc.", "org.apache.", "io.swagger.")

/**
 * Which request handlers leave /api/v1, where the session of the core guards them (CORE1 Р8, Р27). A handler is the
 * core's only when its class comes from the same code source as the core itself ([core], the location of
 * server-core): a package name proves nothing, an extension may declare `dev.sard.server.x`. The handlers of the
 * frameworks are not checked either. A handler whose path cannot be determined counts as outside /api/v1.
 */
internal class ExtensionControllers(
    private val core: URL?,
) {
    /** The paths outside /api/v1 of the handlers that are neither the core's nor a framework's, with their class. */
    fun outsideApi(handlers: List<Pair<RequestMappingInfo, HandlerMethod>>): List<String> =
        handlers
            .filter { (_, handler) -> isChecked(handler.beanType) }
            .flatMap { (info, handler) ->
                pathsOf(info).filterNot { it.startsWith(API_PREFIX) }.map { "$it (${handler.beanType.name})" }
            }

    private fun pathsOf(info: RequestMappingInfo): Set<String> =
        info.pathPatternsCondition
            ?.patternValues
            ?.takeIf { it.isNotEmpty() }
            ?: setOf(NO_PATH)

    private fun isChecked(type: Class<*>): Boolean = !isFramework(type.name) && !isCore(type)

    private fun isFramework(name: String) = FRAMEWORK_PACKAGES.any { name.startsWith(it) }

    private fun isCore(type: Class<*>): Boolean {
        val location = type.protectionDomain?.codeSource?.location
        return core != null && location != null && location.toString() == core.toString()
    }
}
