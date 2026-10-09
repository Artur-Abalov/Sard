// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.Parameter
import io.swagger.v3.oas.annotations.headers.Header
import io.swagger.v3.oas.annotations.media.Content
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.responses.ApiResponse
import io.swagger.v3.oas.annotations.tags.Tag
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.springframework.http.HttpHeaders
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.CookieValue
import org.springframework.web.bind.annotation.PutMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RestController
import tools.jackson.databind.ObjectMapper

/** PUT /api/v1/session/password (F4a, К5): the administrator changes the password from the console. */
@RestController
@RequestMapping("/api/v1/session/password")
@Tag(name = "session")
class PasswordController(
    private val api: SessionApi,
    private val objectMapper: ObjectMapper,
) {
    @PutMapping(consumes = [MediaType.APPLICATION_JSON_VALUE])
    @Operation(
        summary = "Change the administrator password",
        description =
            "Ends every other session; the session that asks goes on under a new id. A wrong current password " +
                "counts as a failed sign-in attempt.",
    )
    @ApiResponse(
        responseCode = "204",
        description = "Changed",
        headers = [
            Header(
                name = "Set-Cookie",
                description = "sard_session; HttpOnly; SameSite=Strict; Path=/; Secure when the connection is HTTPS",
                schema = Schema(type = "string"),
            ),
        ],
    )
    @ApiResponse(
        responseCode = "422",
        description =
            "wrong_password: the current password is not the password; validation_failed: a field is missing " +
                "or the new password is not 12 to 1024 characters",
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = ValidationProblem::class))],
    )
    @ApiResponse(
        responseCode = "429",
        description = "Too many failed attempts; the password is not checked",
        headers = [
            Header(
                name = "Retry-After",
                description = "Seconds until the next attempt",
                schema = Schema(type = "integer"),
            ),
        ],
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    @ApiResponse(
        responseCode = "501",
        description = "The administrator is managed outside the server (an extension)",
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    fun changePassword(
        @RequestBody request: PasswordChangeRequest,
        @Parameter(hidden = true) @CookieValue(SESSION_COOKIE, required = false) sessionId: String?,
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) {
        val result =
            api.changePassword(
                sessionId.orEmpty(),
                request.currentPassword,
                request.newPassword,
                httpRequest.remoteAddr,
            )
        respond(result, httpRequest, httpResponse)
    }

    private fun respond(
        result: PasswordChangeResult,
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) {
        when (result) {
            is PasswordChangeResult.Changed -> {
                val cookie = sessionCookie(result.sessionId, httpRequest.isSecure)
                httpResponse.addHeader(HttpHeaders.SET_COOKIE, cookie.toString())
                httpResponse.status = HttpStatus.NO_CONTENT.value()
            }

            is PasswordChangeResult.WrongPassword -> {
                val errors = listOf(FieldError("currentPassword", "is not the current password"))
                writeUnprocessable(httpResponse, objectMapper, ErrorCode.WRONG_PASSWORD, errors)
            }

            is PasswordChangeResult.InvalidField -> {
                val errors = listOf(FieldError(result.field, "is missing or not acceptable"))
                writeUnprocessable(httpResponse, objectMapper, ErrorCode.VALIDATION_FAILED, errors)
            }

            is PasswordChangeResult.Locked -> {
                httpResponse.addHeader(HttpHeaders.RETRY_AFTER, result.retryAfterSeconds.toString())
                val status = HttpStatus.TOO_MANY_REQUESTS.value()
                writeProblem(httpResponse, objectMapper, status, "Too Many Requests", ErrorCode.TOO_MANY_ATTEMPTS)
            }

            is PasswordChangeResult.NotSupported -> {
                val status = HttpStatus.NOT_IMPLEMENTED.value()
                writeProblem(httpResponse, objectMapper, status, "Not Implemented", ErrorCode.NOT_IMPLEMENTED)
            }
        }
    }
}
