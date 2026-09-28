// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.ErrorCode
import dev.sard.server.api.SESSION_COOKIE
import dev.sard.server.api.clearedSessionCookie
import dev.sard.server.api.writeProblem
import jakarta.servlet.FilterChain
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.springframework.http.HttpHeaders
import org.springframework.web.filter.OncePerRequestFilter
import tools.jackson.databind.ObjectMapper

/** Name of the request attribute [SessionAuthFilter] attaches the touched session under. */
const val SESSION_REQUEST_ATTRIBUTE = "dev.sard.server.auth.session"

/** Operations the filter never guards: sign-in itself, and the public status endpoint. */
internal val PUBLIC_OPERATIONS = setOf("POST /api/v1/session", "GET /api/v1/status")
private const val HTTP_UNAUTHORIZED = 401

/**
 * Guards every path under /api/v1 but sign-in and status (rule "Всё API под /api/v1
 * требует сессию"): a request without a valid [SESSION_COOKIE] gets 401; a valid one
 * is touched (Р1) and attached to the request for the controller to read.
 */
class SessionAuthFilter(
    private val sessionStore: SessionStore,
    private val objectMapper: ObjectMapper,
) : OncePerRequestFilter() {
    override fun doFilterInternal(
        request: HttpServletRequest,
        response: HttpServletResponse,
        filterChain: FilterChain,
    ) {
        val operation = "${request.method} ${request.requestURI}"
        if (operation in PUBLIC_OPERATIONS) {
            filterChain.doFilter(request, response)
            return
        }
        val cookieValue =
            request.cookies
                .orEmpty()
                .firstOrNull { it.name == SESSION_COOKIE }
                ?.value
        val session = cookieValue?.let { sessionStore.touch(it) }
        if (session == null) {
            respondUnauthorized(request, response)
            return
        }
        request.setAttribute(SESSION_REQUEST_ATTRIBUTE, session)
        filterChain.doFilter(request, response)
    }

    /** Р12: DELETE /api/v1/session with an invalid or missing session also clears the cookie. */
    private fun respondUnauthorized(
        request: HttpServletRequest,
        response: HttpServletResponse,
    ) {
        if (request.method == "DELETE" && request.requestURI == "/api/v1/session") {
            response.addHeader(HttpHeaders.SET_COOKIE, clearedSessionCookie(request.isSecure).toString())
        }
        writeProblem(response, objectMapper, HTTP_UNAUTHORIZED, "Unauthorized", ErrorCode.UNAUTHENTICATED)
    }
}
