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
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.DeleteMapping
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.ResponseStatus
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

/** Administrator session in a cookie (D2); W1b implements it. */
@RestController
@RequestMapping("/api/v1/session")
@Tag(name = "session", description = "Administrator sign-in")
class SessionController {
    @PostMapping(consumes = [MediaType.APPLICATION_JSON_VALUE])
    @ResponseStatus(HttpStatus.NO_CONTENT)
    @SecurityRequirements
    @Operation(summary = "Sign in", description = "Sets the session cookie. The same answer for any wrong password.")
    @ApiResponse(
        responseCode = "204",
        description = "Signed in",
        headers = [
            Header(name = "Set-Cookie", description = "sard_session; HttpOnly; SameSite=Strict", schema = Schema(type = "string")),
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
        headers = [Header(name = "Retry-After", description = "Seconds until the next attempt", schema = Schema(type = "integer"))],
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    fun createSession(
        @RequestBody request: SessionRequest,
    ): Unit = notImplemented()

    @GetMapping(produces = [MediaType.APPLICATION_JSON_VALUE])
    @Operation(summary = "Current session", description = "401 when there is none or it expired.")
    fun getSession(): Session = notImplemented()

    @DeleteMapping
    @ResponseStatus(HttpStatus.NO_CONTENT)
    @Operation(summary = "Sign out", description = "Ends the session and clears the cookie.")
    @ApiResponse(responseCode = "204", description = "Signed out")
    fun deleteSession(): Unit = notImplemented()
}
