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
import org.springframework.web.servlet.HandlerMapping
import org.springframework.web.servlet.config.annotation.InterceptorRegistry
import org.springframework.web.servlet.config.annotation.WebMvcConfigurer
import java.util.UUID

/** The methods that change nothing; every other method is a change and is gated (a deny-list would miss PATCH). */
private val SAFE = setOf("GET", "HEAD", "OPTIONS", "TRACE")
private const val ISSUE_TOKEN = "POST /api/v1/enrollment-tokens"
private const val ONBOARDING_PATH = "/api/v1/onboarding/"
private const val SOURCE_SCHEDULE = "PUT /api/v1/sources/{sourceId}/schedule"
private const val AGENT_REVOKE = "POST /api/v1/agents/{agentId}/revoke"

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

private fun idOf(
    variables: Map<String, String>,
    name: String,
): UUID? = variables[name]?.let { runCatching { UUID.fromString(it) }.getOrNull() }

/**
 * Р22: which mutating requests each restriction lets through. The decision is made on the template the request
 * matched, never on the raw URI, so `;x=1`, `%2D` and `..` cannot change what is asked about.
 */
internal object OnboardingRules {
    fun allowed(
        method: String,
        template: String,
        variables: Map<String, String>,
        restriction: OnboardingRestriction,
        targets: GuardTargets,
    ): Boolean =
        when (restriction) {
            OnboardingRestriction.NONE -> true
            OnboardingRestriction.NEW_AGENTS -> "$method $template" != ISSUE_TOKEN
            OnboardingRestriction.FULL -> openToFull(method, template, variables, targets)
        }

    private fun openToFull(
        method: String,
        template: String,
        variables: Map<String, String>,
        targets: GuardTargets,
    ): Boolean =
        "$method $template" in ALWAYS_OPEN ||
            template.startsWith(ONBOARDING_PATH) ||
            systemSchedule(method, template, variables, targets) ||
            builtinRevocation(method, template, variables, targets)

    /** The schedule of a system source (OQ-209). */
    private fun systemSchedule(
        method: String,
        template: String,
        variables: Map<String, String>,
        targets: GuardTargets,
    ): Boolean =
        "$method $template" == SOURCE_SCHEDULE &&
            idOf(variables, "sourceId")?.let(targets::isSystemSource) == true

    /** The revocation of the live built-in agent, which the REST layer asks to confirm (OQ-209). */
    private fun builtinRevocation(
        method: String,
        template: String,
        variables: Map<String, String>,
        targets: GuardTargets,
    ): Boolean =
        "$method $template" == AGENT_REVOKE &&
            idOf(variables, "agentId")?.let(targets::isBuiltinAgent) == true

    /** Whether the operation of the contract can be refused by some restriction: the document says so. */
    fun gated(
        method: String,
        template: String,
    ): Boolean = method !in SAFE && "$method $template" !in ALWAYS_OPEN && !template.startsWith(ONBOARDING_PATH)
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
        if (request.method !in SAFE && !permitted(request)) throw OnboardingIncomplete()
        return true
    }

    private fun permitted(request: HttpServletRequest): Boolean {
        val restriction = onboarding.restriction()
        // Defensive: every Spring MVC mapping sets the pattern; without one there is nothing to decide on, so refuse.
        val template = request.getAttribute(HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE) as? String
        val variables = variablesOf(request)
        return restriction == OnboardingRestriction.NONE ||
            (template != null && OnboardingRules.allowed(request.method, template, variables, restriction, targets))
    }

    private fun variablesOf(request: HttpServletRequest): Map<String, String> {
        val attribute = request.getAttribute(HandlerMapping.URI_TEMPLATE_VARIABLES_ATTRIBUTE)
        return (attribute as? Map<*, *>)?.entries?.associate { it.key.toString() to it.value.toString() }.orEmpty()
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

/** The body of the 409: the operation's own, or the plain problem, or either of them. */
private fun conflictBody(
    own: ApiResponse?,
    problem: Schema<Any>,
): Schema<Any> {
    val ownSchema = own?.content?.get(PROBLEM_JSON)?.schema ?: return problem
    return Schema<Any>().anyOf(listOf(ownSchema, problem))
}

private fun conflictOf(
    own: ApiResponse?,
    problem: Schema<Any>,
): ApiResponse {
    val content = Content().addMediaType(PROBLEM_JSON, MediaType().schema(conflictBody(own, problem)))
    val description = own?.description ?: "The onboarding is not complete (onboarding_incomplete)"
    return ApiResponse().description("$description; or the onboarding is not complete").content(content)
}
