// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import com.fasterxml.jackson.annotation.JsonProperty
import io.swagger.v3.oas.annotations.media.Schema
import jakarta.servlet.http.HttpServletResponse
import org.springframework.http.HttpStatus
import org.springframework.http.ProblemDetail
import org.springframework.web.ErrorResponseException
import tools.jackson.databind.ObjectMapper
import java.util.UUID

/** Media type of every error body (RFC 9457). */
const val PROBLEM_JSON = "application/problem+json"

/** Machine-readable reason of an error; the UI picks its message by this code, not by the title. */
@Schema(enumAsRef = true)
enum class ErrorCode {
    @JsonProperty("unauthenticated")
    UNAUTHENTICATED,

    @JsonProperty("too_many_attempts")
    TOO_MANY_ATTEMPTS,

    @JsonProperty("not_found")
    NOT_FOUND,

    @JsonProperty("validation_failed")
    VALIDATION_FAILED,

    @JsonProperty("unknown_agent")
    UNKNOWN_AGENT,

    @JsonProperty("unknown_plugin")
    UNKNOWN_PLUGIN,

    @JsonProperty("unknown_repository")
    UNKNOWN_REPOSITORY,

    @JsonProperty("invalid_config")
    INVALID_CONFIG,

    @JsonProperty("run_active")
    RUN_ACTIVE,

    @JsonProperty("token_used")
    TOKEN_USED,

    @JsonProperty("token_expired")
    TOKEN_EXPIRED,

    @JsonProperty("not_implemented")
    NOT_IMPLEMENTED,

    @JsonProperty("origin_rejected")
    ORIGIN_REJECTED,
}

private const val TYPE = "URI reference identifying the problem type; about:blank when the status says it all"
private const val TITLE = "Short summary for people; not for matching, use code"
private const val DETAIL = "Explanation of this occurrence; null when the title says it all"

@Schema(description = "An error (RFC 9457)")
data class Problem(
    @field:Schema(description = TYPE) val type: String,
    @field:Schema(description = TITLE) val title: String,
    @field:Schema(description = "HTTP status code") val status: Int,
    @field:Schema(description = DETAIL) val detail: String?,
    val code: ErrorCode,
)

@Schema(description = "A field of the request that failed validation")
data class FieldError(
    @field:Schema(description = "JSON name of the field, e.g. repositoryName")
    val field: String,
    @field:Schema(description = "Why the value was rejected")
    val message: String,
)

@Schema(description = "The request is well-formed but its values are not acceptable (RFC 9457)")
data class ValidationProblem(
    @field:Schema(description = TYPE) val type: String,
    @field:Schema(description = TITLE) val title: String,
    @field:Schema(description = "HTTP status code") val status: Int,
    @field:Schema(description = DETAIL) val detail: String?,
    val code: ErrorCode,
    @field:Schema(description = "The rejected fields") val errors: List<FieldError>,
)

@Schema(description = "The source already has an active run (D6); no new run was created (RFC 9457)")
data class RunActiveProblem(
    @field:Schema(description = TYPE) val type: String,
    @field:Schema(description = TITLE) val title: String,
    @field:Schema(description = "HTTP status code") val status: Int,
    @field:Schema(description = DETAIL) val detail: String?,
    @field:Schema(description = "run_active") val code: ErrorCode,
    @field:Schema(description = "The run that is queued, dispatched or running")
    val activeRunId: UUID,
)

@Schema(description = "The token can no longer be revoked (RFC 9457)")
data class TokenConflictProblem(
    @field:Schema(description = TYPE) val type: String,
    @field:Schema(description = TITLE) val title: String,
    @field:Schema(description = "HTTP status code") val status: Int,
    @field:Schema(description = DETAIL) val detail: String?,
    @field:Schema(description = "token_used or token_expired") val code: ErrorCode,
    @field:Schema(description = "The agent enrolled with the token; set when code is token_used")
    val agentId: UUID?,
)

/** Body of a stub whose behavior S8b implements. */
fun notImplemented(): Nothing {
    val problem = ProblemDetail.forStatus(HttpStatus.NOT_IMPLEMENTED)
    problem.setProperty("code", "not_implemented")
    throw ErrorResponseException(HttpStatus.NOT_IMPLEMENTED, problem, null)
}

/**
 * Writes an RFC 9457 problem body directly to a servlet response: the single writer for
 * every filter that must answer before Spring MVC even picks a handler (session auth,
 * CSRF's Origin guard) and for controller code with a status Spring cannot infer from
 * the method's return type alone (sign-in's 204/401/429).
 */
fun writeProblem(
    response: HttpServletResponse,
    objectMapper: ObjectMapper,
    status: Int,
    title: String,
    code: ErrorCode,
) {
    val problem = Problem(type = "about:blank", title = title, status = status, detail = null, code = code)
    response.status = status
    // Set directly (not response.characterEncoding), so Tomcat does not append ";charset=..." to it:
    // the contract's Content-Type is exactly "application/problem+json".
    response.setHeader("Content-Type", PROBLEM_JSON)
    response.outputStream.write(objectMapper.writeValueAsBytes(problem))
}
