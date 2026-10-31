// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import jakarta.servlet.FilterChain
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.springframework.http.HttpHeaders
import org.springframework.web.filter.OncePerRequestFilter

private const val NO_SNIFF = "nosniff"
private const val NO_STORE = "no-store"
private const val CONTENT_TYPE_OPTIONS = "X-Content-Type-Options"

/**
 * The two headers of every response under /api/v1 (CORE1 Р47): `X-Content-Type-Options: nosniff` and
 * `Cache-Control: no-store`, one of each. The only headers the chain writes; the others of Spring Security are off.
 */
internal fun addApiHeaders(response: HttpServletResponse) {
    response.setHeader(CONTENT_TYPE_OPTIONS, NO_SNIFF)
    response.setHeader(HttpHeaders.CACHE_CONTROL, NO_STORE)
}

/**
 * Sets the headers of the API before anything else of the chain answers, so the Origin rejection, 401 and 403 carry
 * them as the answers of the handlers do. A response outside /api/v1 is left to whoever serves it.
 */
class ApiHeadersFilter : OncePerRequestFilter() {
    override fun doFilterInternal(
        request: HttpServletRequest,
        response: HttpServletResponse,
        filterChain: FilterChain,
    ) {
        if (isApiPath(request.requestURI)) addApiHeaders(response)
        filterChain.doFilter(request, response)
    }
}
