// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.ErrorCode
import dev.sard.server.api.SESSION_COOKIE
import dev.sard.server.api.SESSION_REQUEST_ATTRIBUTE
import dev.sard.server.api.clearedSessionCookie
import dev.sard.server.api.writeProblem
import jakarta.servlet.FilterChain
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.springframework.http.HttpHeaders
import org.springframework.web.filter.OncePerRequestFilter
import tools.jackson.databind.ObjectMapper

/**
 * Operations whose security requirement is empty: sign-in itself, the public status endpoint, and the two the
 * first-start wizard opens without a session (F4a, К1, К2). The contract test compares this set with OpenAPI.
 */
internal val PUBLIC_OPERATIONS =
    setOf(
        "POST /api/v1/session",
        "GET /api/v1/status",
        "GET /api/v1/onboarding",
        "POST /api/v1/onboarding/setup-session",
    )

/** Operations for which the filter does not even look at a session. */
private val SESSIONLESS_OPERATIONS =
    setOf("POST /api/v1/session", "GET /api/v1/status", "POST /api/v1/onboarding/setup-session")

/**
 * The state of the wizard (К1) is public and tells which session came with the request; the steps (К3, К4) ask for
 * a setup session, which the controller checks because the filter does not know setup sessions. All three pass
 * the filter with or without an administrator session; a valid one is attached to the request.
 */
private val WIZARD_OPERATIONS =
    setOf("GET /api/v1/onboarding", "POST /api/v1/onboarding/ca", "POST /api/v1/onboarding/admin")
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
        if (operation in SESSIONLESS_OPERATIONS) {
            filterChain.doFilter(request, response)
            return
        }
        val cookieValue =
            request.cookies
                .orEmpty()
                .firstOrNull { it.name == SESSION_COOKIE }
                ?.value
        val session = cookieValue?.let { sessionStore.touch(it) }
        if (session == null && operation !in WIZARD_OPERATIONS) {
            respondUnauthorized(request, response)
            return
        }
        session?.let { request.setAttribute(SESSION_REQUEST_ATTRIBUTE, it) }
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
