// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.media.Content
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.responses.ApiResponse
import io.swagger.v3.oas.annotations.tags.Tag
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PathVariable
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RequestParam
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import java.time.Instant
import java.util.UUID

@Schema(description = "A new enrollment token")
data class CreateEnrollmentTokenRequest(
    @field:Schema(
        description = "Lifetime in seconds: from 5 minutes to 7 days; 24 hours when omitted",
        minimum = "300",
        maximum = "604800",
    )
    val ttlSeconds: Long? = null,
    @field:Schema(
        description = "What the token is for, shown in the list; up to 200 characters, empty means none",
        maxLength = 200,
    )
    val label: String? = null,
)

@Schema(description = "A created token: the only response with the token string (docs/specs/enrollment-token.md)")
data class CreatedEnrollmentToken(
    val id: UUID,
    @field:Schema(description = "sard_<secret>.<CA fingerprint>; shown once, the server keeps only its hash")
    val token: String,
    @field:Schema(
        description = "Ready-to-run command: sudo -u sard-agent sard-agent enroll --server <address> --token <token>",
    )
    val enrollCommand: String,
    val expiresAt: Instant,
    @field:Schema(description = "false when the address in the command is derived from the server names, not set")
    val agentEndpointConfigured: Boolean,
) {
    // Never the token: Spring logs the body it writes at DEBUG.
    override fun toString() = "CreatedEnrollmentToken(id=$id, expiresAt=$expiresAt)"
}

@Schema(description = "An enrollment token without its string")
data class EnrollmentToken(
    val id: UUID,
    val status: EnrollmentTokenStatus,
    val createdAt: Instant,
    val expiresAt: Instant,
    @field:Schema(description = "When an agent enrolled with it")
    val usedAt: Instant?,
    val revokedAt: Instant?,
    @field:Schema(description = "The agent enrolled with it; set when status is used")
    val agentId: UUID?,
    @field:Schema(description = "What the token is for; null when it has no label")
    val label: String?,
)

@Schema(description = "A page of enrollment tokens")
data class EnrollmentTokenPage(
    val items: List<EnrollmentToken>,
    @field:Schema(description = CURSOR_NEXT)
    val nextCursor: String?,
)

/** What the token endpoints do; S2b defines the behavior and implements it. */
interface EnrollmentTokensApi {
    fun createEnrollmentToken(request: CreateEnrollmentTokenRequest): CreatedEnrollmentToken

    fun listEnrollmentTokens(
        status: EnrollmentTokenStatus?,
        cursor: String?,
        limit: Int,
    ): EnrollmentTokenPage

    fun getEnrollmentToken(tokenId: UUID): EnrollmentToken

    fun revokeEnrollmentToken(tokenId: UUID): EnrollmentToken
}

@RestController
@RequestMapping("/api/v1/enrollment-tokens", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "enrollment-tokens")
class EnrollmentTokensController(
    private val api: EnrollmentTokensApi,
) {
    @PostMapping(consumes = [MediaType.APPLICATION_JSON_VALUE])
    @ResponseStatus(HttpStatus.CREATED)
    @Operation(summary = "Create a token", description = "The token string and command are in this response only.")
    @Unprocessable
    fun createEnrollmentToken(
        @RequestBody request: CreateEnrollmentTokenRequest,
    ): CreatedEnrollmentToken = api.createEnrollmentToken(request)

    @GetMapping
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "List tokens", description = "Newest first.")
    @Unprocessable
    fun listEnrollmentTokens(
        @RequestParam(required = false) status: EnrollmentTokenStatus?,
        @PageCursor @RequestParam(required = false) cursor: String?,
        @PageLimit @RequestParam(defaultValue = DEFAULT_LIMIT) limit: Int,
    ): EnrollmentTokenPage = api.listEnrollmentTokens(status, cursor, limit)

    @GetMapping("/{tokenId}")
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "Token card")
    @NotFound
    fun getEnrollmentToken(
        @PathVariable tokenId: UUID,
    ): EnrollmentToken = api.getEnrollmentToken(tokenId)

    @PostMapping("/{tokenId}/revoke")
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "Revoke a token", description = "Revoking a revoked token is a no-op and answers 200.")
    @NotFound
    @ApiResponse(
        responseCode = "409",
        description = "The token was used (agentId names the agent) or expired",
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = TokenConflictProblem::class))],
    )
    fun revokeEnrollmentToken(
        @PathVariable tokenId: UUID,
    ): EnrollmentToken = api.revokeEnrollmentToken(tokenId)
}
