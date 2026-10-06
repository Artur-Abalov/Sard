// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.console

import jakarta.servlet.FilterChain
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.springframework.web.filter.OncePerRequestFilter

/** Serves the console from a [ConsoleBundle] (Р4-Р6 of docs/specs/server/console-serving.feature). */
class ConsoleFilter(
    private val bundle: ConsoleBundle,
) : OncePerRequestFilter() {
    private val routes = ConsoleRoutes { bundle.file(it) != null }

    override fun doFilterInternal(
        request: HttpServletRequest,
        response: HttpServletResponse,
        filterChain: FilterChain,
    ) {
        when (val route = routeOf(request)) {
            ConsoleRoute.PassThrough -> {
                filterChain.doFilter(request, response)
            }

            ConsoleRoute.MethodNotAllowed -> {
                response.status = HttpServletResponse.SC_METHOD_NOT_ALLOWED
                response.setHeader("Allow", SERVED_METHODS.joinToString(", "))
            }

            ConsoleRoute.NotFound -> {
                response.status = HttpServletResponse.SC_NOT_FOUND
            }

            ConsoleRoute.Page -> {
                send(request, response, "index.html")
            }

            is ConsoleRoute.File -> {
                send(request, response, route.path)
            }
        }
    }

    /** Routes on the container-normalized path (no doubled slashes, no `;x`), as the API filters see it. */
    private fun routeOf(request: HttpServletRequest): ConsoleRoute =
        routes.route(request.method, request.servletPath + (request.pathInfo ?: ""))

    private fun send(
        request: HttpServletRequest,
        response: HttpServletResponse,
        path: String,
    ) {
        val file = checkNotNull(bundle.file(path)) { "console file $path vanished" }
        response.setHeader("Content-Type", contentTypeOf(path))
        response.setHeader("Cache-Control", cacheControlOf(path))
        SECURITY_HEADERS.forEach { (name, value) -> response.setHeader(name, value) }
        response.setContentLengthLong(file.contentLength())
        if (request.method == "GET") file.inputStream.use { it.copyTo(response.outputStream) }
    }

    private fun cacheControlOf(path: String): String = if (path.startsWith("assets/")) IMMUTABLE else "no-cache"
}

/** Р6. HSTS is not set: TLS ends in front of the server (ADR 0039). 'unsafe-inline' for styles is Mantine's need. */
private val SECURITY_HEADERS =
    mapOf(
        "X-Content-Type-Options" to "nosniff",
        "X-Frame-Options" to "DENY",
        "Referrer-Policy" to "same-origin",
        "Content-Security-Policy" to
            "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
            "font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; " +
            "form-action 'self'; frame-ancestors 'none'",
    )

private val CONTENT_TYPES =
    mapOf(
        "html" to "text/html;charset=UTF-8",
        "js" to "text/javascript",
        "css" to "text/css",
        "woff2" to "font/woff2",
        "woff" to "font/woff",
        "svg" to "image/svg+xml",
    )

/** The Content-Type of a bundle file by its extension; Vite's output has no other kinds. */
internal fun contentTypeOf(path: String): String {
    val extension = path.substringAfterLast('.', "")
    return CONTENT_TYPES[extension] ?: "application/octet-stream"
}

private const val IMMUTABLE = "public, max-age=31536000, immutable"
