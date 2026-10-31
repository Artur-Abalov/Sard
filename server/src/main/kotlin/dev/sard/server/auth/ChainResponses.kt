// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.ErrorCode
import dev.sard.server.api.clearedSessionCookie
import dev.sard.server.api.writeProblem
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.springframework.http.HttpHeaders
import org.springframework.security.access.AccessDeniedException
import org.springframework.security.core.AuthenticationException
import org.springframework.security.web.AuthenticationEntryPoint
import org.springframework.security.web.access.AccessDeniedHandler
import org.springframework.security.web.firewall.RequestRejectedException
import org.springframework.security.web.firewall.RequestRejectedHandler
import tools.jackson.databind.ObjectMapper

private const val HTTP_UNAUTHORIZED = 401
private const val HTTP_FORBIDDEN = 403
private const val HTTP_BAD_REQUEST = 400

/**
 * The refusal of a request without an authentication (CORE1 Р38): 401 unauthenticated as problem+json, with no
 * `WWW-Authenticate` (nothing offers Basic). Р12: DELETE /api/v1/session also clears the cookie, as it may be a
 * stale one.
 */
class ProblemAuthenticationEntryPoint(
    private val objectMapper: ObjectMapper,
) : AuthenticationEntryPoint {
    override fun commence(
        request: HttpServletRequest,
        response: HttpServletResponse,
        authException: AuthenticationException,
    ) {
        if ("${request.method} ${request.requestURI}" == SIGN_OUT) {
            response.addHeader(HttpHeaders.SET_COOKIE, clearedSessionCookie(request.isSecure).toString())
        }
        writeProblem(response, objectMapper, HTTP_UNAUTHORIZED, "Unauthorized", ErrorCode.UNAUTHENTICATED)
    }
}

/** The refusal of the access policy of an authenticated request (CORE1 Р38): 403 access_denied as problem+json. */
class ProblemAccessDeniedHandler(
    private val objectMapper: ObjectMapper,
) : AccessDeniedHandler {
    override fun handle(
        request: HttpServletRequest,
        response: HttpServletResponse,
        accessDeniedException: AccessDeniedException,
    ) = writeProblem(response, objectMapper, HTTP_FORBIDDEN, "Forbidden", ErrorCode.ACCESS_DENIED)
}

/**
 * A path the firewall of Spring Security cannot match unambiguously (";", an encoded "%2e", "%2f" or "%5c", not
 * normalized) is refused before any code of the server runs: 400 with no body (CORE1 Р51). The firewall is ahead
 * of the chain, so the headers of the API (Р47) are written here, on any path.
 */
class ApiRequestRejectedHandler : RequestRejectedHandler {
    override fun handle(
        request: HttpServletRequest,
        response: HttpServletResponse,
        requestRejectedException: RequestRejectedException,
    ) {
        addApiHeaders(response)
        response.status = HTTP_BAD_REQUEST
    }
}
