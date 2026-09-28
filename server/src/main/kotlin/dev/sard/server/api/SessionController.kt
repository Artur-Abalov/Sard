// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.headers.Header
import io.swagger.v3.oas.annotations.media.Content
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.responses.ApiResponse
import io.swagger.v3.oas.annotations.security.SecurityRequirements
import io.swagger.v3.oas.annotations.tags.Tag
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.springframework.http.HttpHeaders
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.http.ResponseEntity
import org.springframework.http.converter.HttpMessageNotReadableException
import org.springframework.web.bind.annotation.CookieValue
import org.springframework.web.bind.annotation.DeleteMapping
import org.springframework.web.bind.annotation.ExceptionHandler
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RestController
import java.time.Instant
import java.util.UUID

@Schema(description = "Sign-in with the administrator password (D2)")
data class SessionRequest(
    @field:Schema(description = "The administrator password from the server's environment")
    val password: String,
)

@Schema(description = "The current administrator session")
data class Session(
    @field:Schema(description = "Tenant of the session; every other endpoint works within it")
    val tenantId: UUID,
    @field:Schema(description = "When the session ends unless renewed")
    val expiresAt: Instant,
)

/** The outcome of a sign-in attempt (D2, W1b): what the controller answers, not how. */
sealed interface SignInResult {
    data class SignedIn(
        val sessionId: String,
    ) : SignInResult

    data object WrongPassword : SignInResult

    data class Locked(
        val retryAfterSeconds: Long,
    ) : SignInResult
}

/** No session, or an id that names none that is still valid. */
class NoSuchSessionException : RuntimeException()

/**
 * Administrator session (D2, W1b): a domain port with no HTTP types, so it can be
 * implemented and tested without a servlet request or response, and an enterprise
 * starter can replace it (e.g. with SSO) without depending on this module's web layer.
 */
interface SessionApi {
    /** [previousSessionId] is the id the caller already carried, if any; a successful sign-in ends it. */
    fun createSession(
        password: String,
        clientAddress: String,
        previousSessionId: String?,
    ): SignInResult

    /** @throws NoSuchSessionException when [sessionId] names no session that is still valid. */
    fun getSession(sessionId: String): Session

    /** @throws NoSuchSessionException when [sessionId] names no session that is still valid. */
    fun deleteSession(
        sessionId: String,
        clientAddress: String,
    )
}

/** HTTP side of the session endpoints: maps [SignInResult] and [NoSuchSessionException] to status, cookies and body. */
@RestController
@RequestMapping("/api/v1/session")
@Tag(name = "session", description = "Administrator sign-in")
class SessionController(
    private val api: SessionApi,
    private val objectMapper: tools.jackson.databind.ObjectMapper,
) {
    @PostMapping(consumes = [MediaType.APPLICATION_JSON_VALUE])
    @SecurityRequirements
    @Operation(summary = "Sign in", description = "Sets the session cookie. The same answer for any wrong password.")
    @ApiResponse(
        responseCode = "204",
        description = "Signed in",
        headers = [
            Header(
                name = "Set-Cookie",
                description = "sard_session; HttpOnly; SameSite=Strict; Path=/; Secure when the connection is HTTPS",
                schema = Schema(type = "string"),
            ),
        ],
    )
    @ApiResponse(
        responseCode = "401",
        description = "Wrong password",
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    @ApiResponse(
        responseCode = "429",
        description = "Too many failed attempts; sign-in is locked for a while",
        headers = [
            Header(
                name = "Retry-After",
                description = "Seconds until the next attempt",
                schema = Schema(type = "integer"),
            ),
        ],
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    fun createSession(
        @RequestBody request: SessionRequest,
        @CookieValue(SESSION_COOKIE, required = false) previousSessionId: String?,
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) {
        val result = api.createSession(request.password, httpRequest.remoteAddr, previousSessionId)
        respondToSignIn(result, httpRequest, httpResponse)
    }

    /**
     * A body without a usable password (missing, null, not JSON, empty) is rejected the
     * same as a wrong one (К2): the contract does not change, and the attempt still
     * counts against the client address.
     */
    @ExceptionHandler(HttpMessageNotReadableException::class)
    fun onMalformedSignIn(
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) {
        val previousSessionId =
            httpRequest.cookies
                .orEmpty()
                .firstOrNull { it.name == SESSION_COOKIE }
                ?.value
        respondToSignIn(api.createSession("", httpRequest.remoteAddr, previousSessionId), httpRequest, httpResponse)
    }

    private fun respondToSignIn(
        result: SignInResult,
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) {
        when (result) {
            is SignInResult.SignedIn -> {
                val cookie = sessionCookie(result.sessionId, httpRequest.isSecure)
                httpResponse.addHeader(HttpHeaders.SET_COOKIE, cookie.toString())
                httpResponse.status = HttpStatus.NO_CONTENT.value()
            }

            is SignInResult.WrongPassword -> {
                val status = HttpStatus.UNAUTHORIZED.value()
                writeProblem(httpResponse, objectMapper, status, "Unauthorized", ErrorCode.UNAUTHENTICATED)
            }

            is SignInResult.Locked -> {
                httpResponse.addHeader(HttpHeaders.RETRY_AFTER, result.retryAfterSeconds.toString())
                val status = HttpStatus.TOO_MANY_REQUESTS.value()
                writeProblem(httpResponse, objectMapper, status, "Too Many Requests", ErrorCode.TOO_MANY_ATTEMPTS)
            }
        }
    }

    @GetMapping(produces = [MediaType.APPLICATION_JSON_VALUE])
    @Operation(summary = "Current session", description = "401 when there is none or it expired.")
    fun getSession(
        @CookieValue(SESSION_COOKIE, required = false) sessionId: String?,
    ): ResponseEntity<Session> = ResponseEntity.ok(api.getSession(sessionId ?: throw NoSuchSessionException()))

    @DeleteMapping
    @Operation(summary = "Sign out", description = "Ends the session and clears the cookie.")
    @ApiResponse(responseCode = "204", description = "Signed out")
    fun deleteSession(
        @CookieValue(SESSION_COOKIE, required = false) sessionId: String?,
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) {
        api.deleteSession(sessionId ?: throw NoSuchSessionException(), httpRequest.remoteAddr)
        httpResponse.addHeader(HttpHeaders.SET_COOKIE, clearedSessionCookie(httpRequest.isSecure).toString())
        httpResponse.status = HttpStatus.NO_CONTENT.value()
    }

    /** Р12: an unauthenticated GET/DELETE also clears the cookie, since it may be a stale one. */
    @ExceptionHandler(NoSuchSessionException::class)
    fun onNoSuchSession(
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) {
        httpResponse.addHeader(HttpHeaders.SET_COOKIE, clearedSessionCookie(httpRequest.isSecure).toString())
        val status = HttpStatus.UNAUTHORIZED.value()
        writeProblem(httpResponse, objectMapper, status, "Unauthorized", ErrorCode.UNAUTHENTICATED)
    }
}
