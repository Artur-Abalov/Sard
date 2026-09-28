// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.ErrorCode
import jakarta.servlet.FilterChain
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.springframework.web.filter.OncePerRequestFilter
import tools.jackson.databind.ObjectMapper

private val MUTATING_METHODS = setOf("POST", "PUT", "PATCH", "DELETE")
private const val API_PREFIX = "/api/v1/"
private const val HTTP_FORBIDDEN = 403

/**
 * CSRF defense for /api/v1 (К1, Р5): a mutating request whose Origin does not match
 * this request's own scheme, Host header and port is rejected before any handler
 * runs, so an unmapped mutating path is refused the same way. A request without an
 * Origin header is let through (sardctl, curl); "null" is a foreign origin.
 */
class OriginGuardFilter(
    private val objectMapper: ObjectMapper,
) : OncePerRequestFilter() {
    override fun doFilterInternal(
        request: HttpServletRequest,
        response: HttpServletResponse,
        filterChain: FilterChain,
    ) {
        if (!request.requestURI.startsWith(API_PREFIX) || request.method !in MUTATING_METHODS) {
            filterChain.doFilter(request, response)
            return
        }
        val origin = request.getHeader("Origin")
        if (origin != null && origin != expectedOrigin(request)) {
            writeProblem(response, objectMapper, HTTP_FORBIDDEN, "Forbidden", ErrorCode.ORIGIN_REJECTED)
            return
        }
        filterChain.doFilter(request, response)
    }

    private fun expectedOrigin(request: HttpServletRequest): String {
        val host = request.getHeader("Host") ?: return ""
        val scheme = if (request.isSecure) "https" else "http"
        return "$scheme://$host"
    }
}
