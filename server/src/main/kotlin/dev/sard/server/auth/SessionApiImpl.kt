// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.ErrorCode
import dev.sard.server.api.SessionApi
import dev.sard.server.api.SessionRequest
import dev.sard.server.extension.TenantResolver
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.slf4j.LoggerFactory
import org.springframework.http.HttpHeaders
import org.springframework.http.HttpStatus
import org.springframework.http.ResponseEntity
import org.springframework.stereotype.Component
import tools.jackson.databind.ObjectMapper
import java.time.Clock
import dev.sard.server.api.Session as SessionResponse

private val log = LoggerFactory.getLogger(SessionApiImpl::class.java)

/**
 * Sign-in, current session and sign-out (D2, W1b). Origin and session-presence checks
 * already ran in [OriginGuardFilter] and [SessionAuthFilter]; this class owns the
 * password check, the brute-force lock and the session's own lifecycle.
 */
@Component
class SessionApiImpl(
    private val passwordAuthenticator: AdminPasswordAuthenticator,
    private val sessionStore: SessionStore,
    private val attemptTracker: LoginAttemptTracker,
    private val tenantResolver: TenantResolver,
    private val clock: Clock,
    private val objectMapper: ObjectMapper,
) : SessionApi {
    override fun createSession(
        request: SessionRequest,
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) {
        val address = clientAddress(httpRequest)
        // The whole attempt is one atomic step per address (see withAddressLock): otherwise
        // concurrent requests could all read "not locked" before any of them is recorded.
        attemptTracker.withAddressLock(address) {
            val retryAfter = attemptTracker.retryAfterSeconds(address)
            when {
                retryAfter != null -> respondLocked(httpResponse, retryAfter)
                passwordAuthenticator.matches(request.password) -> respondSignedIn(address, httpRequest, httpResponse)
                else -> respondWrongPassword(address, httpResponse)
            }
        }
    }

    override fun getSession(httpRequest: HttpServletRequest): ResponseEntity<SessionResponse> {
        val session = currentSession(httpRequest)
        return ResponseEntity.ok(SessionResponse(session.tenantId, sessionStore.expiresAt(session)))
    }

    override fun deleteSession(
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) {
        val session = currentSession(httpRequest)
        sessionStore.remove(session.id)
        log.info("Signed out from {}", clientAddress(httpRequest))
        httpResponse.addHeader(HttpHeaders.SET_COOKIE, clearedSessionCookie(httpRequest.isSecure).toString())
        httpResponse.status = HttpStatus.NO_CONTENT.value()
    }

    private fun respondSignedIn(
        address: String,
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) {
        attemptTracker.recordSuccess(address)
        existingSessionId(httpRequest)?.let { sessionStore.remove(it) }
        val session = sessionStore.create(tenantResolver.currentTenantId())
        log.info("Sign-in succeeded from {}", address)
        httpResponse.addHeader(HttpHeaders.SET_COOKIE, sessionCookie(session.id, httpRequest.isSecure).toString())
        httpResponse.status = HttpStatus.NO_CONTENT.value()
    }

    private fun respondWrongPassword(
        address: String,
        httpResponse: HttpServletResponse,
    ) {
        val justLocked = attemptTracker.recordFailure(address)
        if (justLocked) log.warn("Sign-in locked from {}", address)
        log.info("Sign-in failed from {}", address)
        val status = HttpStatus.UNAUTHORIZED.value()
        writeProblem(httpResponse, objectMapper, status, "Unauthorized", ErrorCode.UNAUTHENTICATED)
    }

    private fun currentSession(httpRequest: HttpServletRequest): Session {
        val attribute = httpRequest.getAttribute(SESSION_REQUEST_ATTRIBUTE)
        return attribute as Session
    }

    private fun existingSessionId(httpRequest: HttpServletRequest): String? =
        httpRequest.cookies
            .orEmpty()
            .firstOrNull { it.name == SESSION_COOKIE_NAME }
            ?.value

    private fun respondLocked(
        httpResponse: HttpServletResponse,
        retryAfterSeconds: Long,
    ) {
        httpResponse.addHeader(HttpHeaders.RETRY_AFTER, retryAfterSeconds.toString())
        writeProblem(
            httpResponse,
            objectMapper,
            HttpStatus.TOO_MANY_REQUESTS.value(),
            "Too Many Requests",
            ErrorCode.TOO_MANY_ATTEMPTS,
        )
    }
}
