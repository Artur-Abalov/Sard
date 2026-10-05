// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.console

/** What the console does with one request (rules of Р4 in docs/specs/server/console-serving.feature). */
sealed interface ConsoleRoute {
    /** Not a console path: API, Actuator and OpenAPI answer it. */
    data object PassThrough : ConsoleRoute

    /** A method other than GET and HEAD on a console path. */
    data object MethodNotAllowed : ConsoleRoute

    data object NotFound : ConsoleRoute

    /** The console page, index.html. */
    data object Page : ConsoleRoute

    /** A file of the bundle, [path] relative to its root. */
    data class File(
        val path: String,
    ) : ConsoleRoute
}

/** The methods a console path answers; the Allow header of a 405 lists exactly these. */
val SERVED_METHODS = listOf("GET", "HEAD")
private val FORBIDDEN_CHARS = setOf('\\', '\u0000')
private val DOT_SEGMENTS = setOf("..", ".")
private val OWNED_SEGMENTS = setOf("api", "actuator", "v3")

class ConsoleRoutes(
    private val exists: (String) -> Boolean,
) {
    fun route(
        method: String,
        path: String,
    ): ConsoleRoute =
        when {
            path.split('/').getOrNull(1) in OWNED_SEGMENTS -> ConsoleRoute.PassThrough
            method !in SERVED_METHODS -> ConsoleRoute.MethodNotAllowed
            else -> servedRoute(path)
        }

    private fun servedRoute(path: String): ConsoleRoute =
        when {
            leavesBundle(path) -> ConsoleRoute.NotFound
            path == "/index.html" -> ConsoleRoute.Page
            exists(path.removePrefix("/")) -> ConsoleRoute.File(path.removePrefix("/"))
            isFileName(path) -> ConsoleRoute.NotFound
            else -> ConsoleRoute.Page
        }

    /** A missing file is a 404, never the page: a dot in the last segment, or anywhere under /assets/. */
    private fun isFileName(path: String): Boolean = path.startsWith("/assets/") || '.' in path.substringAfterLast('/')

    /** Dot segments, backslashes and NUL: nothing that could step out of the bundle directory. */
    private fun leavesBundle(path: String): Boolean = hasDotSegment(path) || path.any { it in FORBIDDEN_CHARS }

    private fun hasDotSegment(path: String): Boolean = path.split('/').any { it in DOT_SEGMENTS }
}
