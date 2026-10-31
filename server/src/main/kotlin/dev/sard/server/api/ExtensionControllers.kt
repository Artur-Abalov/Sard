// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import org.springframework.web.method.HandlerMethod
import org.springframework.web.servlet.mvc.method.RequestMappingInfo
import java.net.URL

private const val API_PREFIX = "/api/v1/"
private const val NO_PATH = "<no path>"

/** One class of each framework whose controllers the server serves outside /api/v1 (error page, OpenAPI document). */
private val ANCHOR_CLASSES =
    listOf(
        "org.springframework.boot.webmvc.autoconfigure.error.BasicErrorController",
        "org.springdoc.webmvc.api.OpenApiWebMvcResource",
    )

/**
 * Which request handlers leave /api/v1, where the session of the core guards them (CORE1 Р8, Р27). A handler is
 * trusted only when its class comes from the code source of the core itself ([core], the location of server-core) or
 * of a framework ([anchors]: the locations of the classes in [frameworkLocations]): a package name proves nothing, an
 * extension may declare `dev.sard.server.x` or `org.springframework.x`. A handler whose path cannot be determined
 * counts as outside /api/v1.
 */
internal class ExtensionControllers(
    private val core: URL?,
    private val anchors: Set<URL>,
) {
    /** The paths outside /api/v1 of the handlers that come from neither the core nor a framework, with their class. */
    fun outsideApi(handlers: List<Pair<RequestMappingInfo, HandlerMethod>>): List<String> =
        handlers
            .filterNot { (_, handler) -> isTrusted(handler.beanType) }
            .flatMap { (info, handler) ->
                pathsOf(info).filterNot { it.startsWith(API_PREFIX) }.map { "$it (${handler.beanType.name})" }
            }

    private fun pathsOf(info: RequestMappingInfo): Set<String> =
        info.pathPatternsCondition
            ?.patternValues
            ?.takeIf { it.isNotEmpty() }
            ?: setOf(NO_PATH)

    private fun isTrusted(type: Class<*>): Boolean {
        val location =
            type.protectionDomain
                ?.codeSource
                ?.location
                ?.toString()
        return location != null && (location == core?.toString() || anchors.any { it.toString() == location })
    }

    companion object {
        /** The code sources of the frameworks' controllers; a framework that is not on the class path adds none. */
        fun frameworkLocations(): Set<URL> =
            ANCHOR_CLASSES
                .mapNotNull { name -> runCatching { Class.forName(name) }.getOrNull() }
                .mapNotNull { it.protectionDomain?.codeSource?.location }
                .toSet()
    }
}
