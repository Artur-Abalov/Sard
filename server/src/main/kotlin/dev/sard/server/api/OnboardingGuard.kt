// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.models.OpenAPI
import io.swagger.v3.oas.models.media.Content
import io.swagger.v3.oas.models.media.MediaType
import io.swagger.v3.oas.models.media.Schema
import io.swagger.v3.oas.models.responses.ApiResponse
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.springframework.context.annotation.Configuration
import org.springframework.core.Ordered
import org.springframework.core.annotation.Order
import org.springframework.http.HttpStatus
import org.springframework.web.bind.annotation.ExceptionHandler
import org.springframework.web.bind.annotation.RestControllerAdvice
import org.springframework.web.servlet.HandlerInterceptor
import org.springframework.web.servlet.config.annotation.InterceptorRegistry
import org.springframework.web.servlet.config.annotation.WebMvcConfigurer
import java.util.UUID

private val MUTATING = setOf("POST", "PUT", "DELETE")
private const val ISSUE_TOKEN = "POST /api/v1/enrollment-tokens"
private const val ONBOARDING_PATH = "/api/v1/onboarding/"
private const val UUID_TEXT = "[0-9a-fA-F-]{36}"
private val SOURCE_SCHEDULE = Regex("/api/v1/sources/($UUID_TEXT)/schedule")
private val AGENT_REVOKE = Regex("/api/v1/agents/($UUID_TEXT)/revoke")

/** The mutating operations the full restriction lets through whoever they name (OQ-209). */
private val ALWAYS_OPEN =
    setOf(
        "POST /api/v1/session",
        "DELETE /api/v1/session",
        "PUT /api/v1/session/password",
        "PUT /api/v1/self-backup/repository",
        "POST /api/v1/self-backup/runs",
        "POST /api/v1/self-backup/keys-confirmation",
    )

/** What the guard asks about the objects a path names. */
interface GuardTargets {
    /** [id] is a source of the self-backup. */
    fun isSystemSource(id: UUID): Boolean

    /** [id] is a live built-in agent. */
    fun isBuiltinAgent(id: UUID): Boolean
}

/** A change was asked for while the onboarding is not complete (409 onboarding_incomplete, D8). */
class OnboardingIncomplete : RuntimeException("the onboarding is not complete")

private fun idIn(
    pattern: Regex,
    path: String,
): UUID? = pattern.matchEntire(path)?.let { runCatching { UUID.fromString(it.groupValues[1]) }.getOrNull() }

/** Р22: which mutating requests each restriction lets through. */
internal object OnboardingRules {
    fun allowed(
        method: String,
        path: String,
        restriction: OnboardingRestriction,
        targets: GuardTargets,
    ): Boolean =
        when (restriction) {
            OnboardingRestriction.NONE -> true
            OnboardingRestriction.NEW_AGENTS -> "$method $path" != ISSUE_TOKEN
            OnboardingRestriction.FULL -> openToFull(method, path, targets)
        }

    private fun openToFull(
        method: String,
        path: String,
        targets: GuardTargets,
    ): Boolean {
        val operation = "$method $path"
        return operation in ALWAYS_OPEN ||
            path.startsWith(ONBOARDING_PATH) ||
            (method == "PUT" && idIn(SOURCE_SCHEDULE, path)?.let(targets::isSystemSource) == true) ||
            (method == "POST" && idIn(AGENT_REVOKE, path)?.let(targets::isBuiltinAgent) == true)
    }

    /** Whether the operation of the contract can be refused by some restriction: the document says so. */
    fun gated(
        method: String,
        template: String,
    ): Boolean = method in MUTATING && "$method $template" !in ALWAYS_OPEN && !template.startsWith(ONBOARDING_PATH)
}

/**
 * D8: until the onboarding is complete the server changes nothing but the self-backup and the session (Р22). Runs
 * after the Origin and session filters and before the handler, so a refusal comes before the body is read.
 */
class OnboardingGuard(
    private val onboarding: OnboardingApi,
    private val targets: GuardTargets,
) : HandlerInterceptor {
    override fun preHandle(
        request: HttpServletRequest,
        response: HttpServletResponse,
        handler: Any,
    ): Boolean {
        if (request.method !in MUTATING) return true
        val restriction = onboarding.restriction()
        if (!OnboardingRules.allowed(request.method, request.requestURI, restriction, targets)) {
            throw OnboardingIncomplete()
        }
        return true
    }
}

@Configuration(proxyBeanMethods = false)
class OnboardingGuardConfiguration(
    private val onboarding: OnboardingApi,
    private val targets: GuardTargets,
) : WebMvcConfigurer {
    override fun addInterceptors(registry: InterceptorRegistry) {
        registry.addInterceptor(OnboardingGuard(onboarding, targets)).addPathPatterns("/api/v1/**")
    }
}

/** The refusal of D8. */
@RestControllerAdvice
@Order(Ordered.HIGHEST_PRECEDENCE)
class OnboardingExceptionHandler {
    @ExceptionHandler(OnboardingIncomplete::class)
    fun incomplete() = problemResponse(HttpStatus.CONFLICT, "Conflict", ErrorCode.ONBOARDING_INCOMPLETE)
}

/**
 * Adds 409 onboarding_incomplete to every operation the restriction can refuse (К11). Where the operation has a
 * 409 of its own the response holds either body.
 */
fun describeOnboardingIncomplete(api: OpenAPI) {
    val problem = Schema<Any>().`$ref`("Problem")
    api.paths.orEmpty().forEach { (template, item) ->
        item.readOperationsMap().forEach { (method, operation) ->
            if (OnboardingRules.gated(method.name, template)) {
                val own = operation.responses.get("409")
                operation.responses.addApiResponse("409", conflictOf(own, problem))
            }
        }
    }
}

private fun conflictOf(
    own: ApiResponse?,
    problem: Schema<Any>,
): ApiResponse {
    val ownSchema = own?.content?.get(PROBLEM_JSON)?.schema
    val schema = if (ownSchema == null) problem else Schema<Any>().anyOf(listOf(ownSchema, problem))
    val description = own?.description ?: "The onboarding is not complete (onboarding_incomplete)"
    val content = Content().addMediaType(PROBLEM_JSON, MediaType().schema(schema))
    return ApiResponse().description("$description; or the onboarding is not complete").content(content)
}
