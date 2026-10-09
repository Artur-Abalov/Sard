// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.Parameter
import io.swagger.v3.oas.annotations.headers.Header
import io.swagger.v3.oas.annotations.media.Content
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.responses.ApiResponse
import io.swagger.v3.oas.annotations.security.SecurityRequirement
import io.swagger.v3.oas.annotations.security.SecurityRequirements
import io.swagger.v3.oas.annotations.tags.Tag
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.springframework.http.HttpHeaders
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.http.converter.HttpMessageNotReadableException
import org.springframework.web.bind.annotation.CookieValue
import org.springframework.web.bind.annotation.ExceptionHandler
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import tools.jackson.databind.ObjectMapper

/** The security scheme of the wizard's steps: the setup session cookie (К9). */
const val SETUP_SCHEME = "setupSession"

private const val SESSION_PATH = "/api/v1/onboarding"

private fun HttpServletRequest.cookie(name: String): String? = cookies.orEmpty().firstOrNull { it.name == name }?.value

private fun HttpServletRequest.administratorSession(): Boolean = getAttribute(SESSION_REQUEST_ATTRIBUTE) != null

/** GET /api/v1/onboarding (К1): public; the CA only with a setup or administrator session. */
@RestController
@RequestMapping(SESSION_PATH)
@Tag(name = "onboarding", description = "The first-start wizard")
class OnboardingController(
    private val api: OnboardingApi,
) {
    @GetMapping(produces = [MediaType.APPLICATION_JSON_VALUE])
    @SecurityRequirements
    @Operation(
        operationId = "getOnboarding",
        summary = "State of the first start",
        description =
            "Public. Which steps are done, whether a setup code was issued, and which session came with the " +
                "request. The CA is shown only to a setup or administrator session.",
    )
    fun state(
        @Parameter(hidden = true) @CookieValue(SETUP_COOKIE, required = false) setupSessionId: String?,
        httpRequest: HttpServletRequest,
    ): Onboarding = api.state(httpRequest.administratorSession(), setupSessionId)
}

/** POST /api/v1/onboarding/setup-session (К2): the setup code buys a setup session. */
@RestController
@RequestMapping("$SESSION_PATH/setup-session")
@Tag(name = "onboarding")
class SetupSessionController(
    private val api: OnboardingApi,
    private val objectMapper: ObjectMapper,
) {
    @PostMapping(consumes = [MediaType.APPLICATION_JSON_VALUE])
    @SecurityRequirements
    @Operation(
        operationId = "createSetupSession",
        summary = "Enter the setup code",
        description =
            "The code is printed in the log of the server. A wrong, expired, missing or malformed code is the same " +
                "401 and counts against the client address.",
    )
    @ApiResponse(
        responseCode = "204",
        description = "The code is right; a setup session was issued",
        headers = [
            Header(
                name = "Set-Cookie",
                description =
                    "sard_setup; HttpOnly; SameSite=Strict; Path=/api/v1/onboarding; Secure when the connection is " +
                        "HTTPS. No Max-Age: the session ends with the code, the admin step or a restart.",
                schema = Schema(type = "string"),
            ),
        ],
    )
    @ApiResponse(
        responseCode = "401",
        description = "Wrong, expired or missing code",
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    @ApiResponse(
        responseCode = "409",
        description = "The admin step is done, so there is no code (setup_completed)",
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    @ApiResponse(
        responseCode = "429",
        description = "Too many wrong codes from this address; the code is not checked",
        headers = [
            Header(
                name = "Retry-After",
                description = "Seconds until the next attempt",
                schema = Schema(type = "integer"),
            ),
        ],
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    fun enterCode(
        @RequestBody request: SetupCodeRequest,
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) = respond(request.code, httpRequest, httpResponse)

    /** A body without a code (missing, null, not JSON, empty) is a wrong code and counts as one (К2). */
    @ExceptionHandler(HttpMessageNotReadableException::class)
    fun onMalformed(
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) = respond(null, httpRequest, httpResponse)

    private fun respond(
        code: String?,
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) {
        val result = api.enterCode(code, httpRequest.remoteAddr, httpRequest.cookie(SETUP_COOKIE))
        when (result) {
            is CodeResult.Accepted -> {
                val cookie = setupCookie(result.setupSessionId, httpRequest.isSecure)
                httpResponse.addHeader(HttpHeaders.SET_COOKIE, cookie.toString())
                httpResponse.status = HttpStatus.NO_CONTENT.value()
            }

            is CodeResult.Rejected -> {
                val status = HttpStatus.UNAUTHORIZED.value()
                writeProblem(httpResponse, objectMapper, status, "Unauthorized", ErrorCode.UNAUTHENTICATED)
            }

            is CodeResult.Completed -> {
                val status = HttpStatus.CONFLICT.value()
                writeProblem(httpResponse, objectMapper, status, "Conflict", ErrorCode.SETUP_COMPLETED)
            }

            is CodeResult.Locked -> {
                httpResponse.addHeader(HttpHeaders.RETRY_AFTER, result.retryAfterSeconds.toString())
                val status = HttpStatus.TOO_MANY_REQUESTS.value()
                writeProblem(httpResponse, objectMapper, status, "Too Many Requests", ErrorCode.TOO_MANY_ATTEMPTS)
            }
        }
    }
}

/** POST /api/v1/onboarding/ca (К3): the owner confirms the CA the wizard showed. */
@RestController
@RequestMapping("$SESSION_PATH/ca")
@Tag(name = "onboarding")
class OnboardingCaController(
    private val api: OnboardingApi,
    private val objectMapper: ObjectMapper,
) {
    @PostMapping
    @SecurityRequirement(name = SETUP_SCHEME)
    @Operation(
        operationId = "confirmOnboardingCa",
        summary = "Use the CA of this server",
        description = "Does the step ca; repeating it changes nothing. From now on the CA cannot be replaced.",
    )
    @ApiResponse(responseCode = "204", description = "The step ca is done")
    @ApiResponse(
        responseCode = "401",
        description = "No setup session, or it ended",
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    @ResponseStatus(HttpStatus.NO_CONTENT)
    fun confirm(httpRequest: HttpServletRequest) {
        api.confirmCa(httpRequest.cookie(SETUP_COOKIE), httpRequest.administratorSession(), httpRequest.remoteAddr)
    }

    @ExceptionHandler(NoSuchSessionException::class)
    fun onNoSession(httpResponse: HttpServletResponse) {
        val status = HttpStatus.UNAUTHORIZED.value()
        writeProblem(httpResponse, objectMapper, status, "Unauthorized", ErrorCode.UNAUTHENTICATED)
    }
}

/** POST /api/v1/onboarding/admin (К4): the owner sets the administrator password and is signed in. */
@RestController
@RequestMapping("$SESSION_PATH/admin")
@Tag(name = "onboarding")
class OnboardingAdminController(
    private val api: OnboardingApi,
    private val objectMapper: ObjectMapper,
) {
    @PostMapping(consumes = [MediaType.APPLICATION_JSON_VALUE])
    @SecurityRequirement(name = SETUP_SCHEME)
    @Operation(
        operationId = "completeOnboardingAdmin",
        summary = "Set the administrator password",
        description =
            "Does the step admin and signs the owner in. The code and every setup session end. The password is " +
                "stored as an Argon2id hash.",
    )
    @ApiResponse(
        responseCode = "204",
        description = "The password is set",
        headers = [
            Header(
                name = "Set-Cookie",
                description =
                    "sard_session; HttpOnly; SameSite=Strict; Path=/; Secure when the connection is HTTPS. " +
                        "A second Set-Cookie clears sard_setup.",
                schema = Schema(type = "string"),
            ),
        ],
    )
    @ApiResponse(
        responseCode = "401",
        description = "No setup session, or it ended",
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    @ApiResponse(
        responseCode = "409",
        description = "ca_step_pending: the CA was not confirmed yet; setup_completed: there is an administrator",
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
    )
    @ApiResponse(
        responseCode = "422",
        description = "The password is missing or not 12 to 1024 characters; errors name the field",
        content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = ValidationProblem::class))],
    )
    fun complete(
        @RequestBody request: AdminStepRequest,
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) = respond(request.password, httpRequest, httpResponse)

    /** A body that is no JSON has no password (К4): 422 at the field password. */
    @ExceptionHandler(HttpMessageNotReadableException::class)
    fun onMalformed(
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) = respond(null, httpRequest, httpResponse)

    private fun respond(
        password: String?,
        httpRequest: HttpServletRequest,
        httpResponse: HttpServletResponse,
    ) {
        val setupSessionId = httpRequest.cookie(SETUP_COOKIE)
        // Answered here, not by an exception handler: the handler of a malformed body comes through here too.
        val result =
            try {
                api.completeAdmin(setupSessionId, password, httpRequest.remoteAddr)
            } catch (_: NoSuchSessionException) {
                val status = HttpStatus.UNAUTHORIZED.value()
                writeProblem(httpResponse, objectMapper, status, "Unauthorized", ErrorCode.UNAUTHENTICATED)
                return
            }
        when (result) {
            is AdminStepResult.Done -> {
                val secure = httpRequest.isSecure
                httpResponse.addHeader(HttpHeaders.SET_COOKIE, sessionCookie(result.sessionId, secure).toString())
                httpResponse.addHeader(HttpHeaders.SET_COOKIE, clearedSetupCookie(secure).toString())
                httpResponse.status = HttpStatus.NO_CONTENT.value()
            }

            is AdminStepResult.CaPending -> {
                val status = HttpStatus.CONFLICT.value()
                writeProblem(httpResponse, objectMapper, status, "Conflict", ErrorCode.CA_STEP_PENDING)
            }

            is AdminStepResult.Completed -> {
                val status = HttpStatus.CONFLICT.value()
                writeProblem(httpResponse, objectMapper, status, "Conflict", ErrorCode.SETUP_COMPLETED)
            }

            is AdminStepResult.InvalidPassword -> {
                val errors = listOf(FieldError("password", "is missing or not 12 to 1024 characters"))
                writeUnprocessable(httpResponse, objectMapper, ErrorCode.VALIDATION_FAILED, errors)
            }
        }
    }
}
