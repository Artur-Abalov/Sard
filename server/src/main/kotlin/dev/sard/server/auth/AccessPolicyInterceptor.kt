// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.ErrorCode
import dev.sard.server.api.SESSION_REQUEST_ATTRIBUTE
import dev.sard.server.api.writeProblem
import dev.sard.server.extension.AccessPolicy
import dev.sard.server.extension.Operation
import dev.sard.server.extension.SUBJECT_REQUEST_ATTRIBUTE
import dev.sard.server.extension.Subject
import dev.sard.server.extension.SubjectSource
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.slf4j.LoggerFactory
import org.springframework.beans.factory.ObjectProvider
import org.springframework.web.servlet.HandlerInterceptor
import org.springframework.web.servlet.HandlerMapping
import tools.jackson.databind.ObjectMapper

private val log = LoggerFactory.getLogger(AccessPolicyInterceptor::class.java)
private const val HTTP_UNAUTHORIZED = 401
private const val HTTP_FORBIDDEN = 403
private const val SIGN_OUT = "DELETE /api/v1/session"

/**
 * Asks the [AccessPolicy] after the Origin (403) and the session (401) have been checked. A request that is asked
 * about must carry a core session, or the subject a replaced session filter (Е3) put under
 * [SUBJECT_REQUEST_ATTRIBUTE]: without either it is refused with 401 whatever the [SubjectSource] would say (Р30),
 * so the source names the subject and never authenticates. The subject the policy is asked about is the source's;
 * none is 401 (closed), a source that throws is 403 access_denied with one WARN (Р31).
 * It is an interceptor, not a filter, because the operation is named by its path template, which the handler
 * mapping finds. The public operations, the wizard's and sign-out are not subject to it (Р17).
 * A policy that throws refuses: the failure is closed (Р16). The beans come through providers: a configurer that
 * needs them directly makes Spring create them while the MVC configuration is built, before their post-processing.
 */
class AccessPolicyInterceptor(
    private val policy: ObjectProvider<AccessPolicy>,
    private val subjects: ObjectProvider<SubjectSource>,
    private val objectMapper: ObjectProvider<ObjectMapper>,
) : HandlerInterceptor {
    override fun preHandle(
        request: HttpServletRequest,
        response: HttpServletResponse,
        handler: Any,
    ): Boolean {
        val refusal = operationOf(request)?.let { refusalOf(request, it) }
        refusal?.let { writeProblem(response, objectMapper.getObject(), it.status, it.title, it.code) }
        return refusal == null
    }

    /** Why the request is refused; null when it passes. No core session on an operation that is asked about is 401. */
    private fun refusalOf(
        request: HttpServletRequest,
        operation: Operation,
    ): Refusal? {
        if (!authenticated(request)) return Refusal.UNAUTHENTICATED
        return runCatching { subjects.getObject().current() }.fold(
            onSuccess = { refusalFor(it, operation) },
            onFailure = {
                log.warn("The subject source failed on {}, access is denied: {}", operation, it.message)
                Refusal.DENIED
            },
        )
    }

    private fun refusalFor(
        subject: Subject?,
        operation: Operation,
    ): Refusal? =
        when {
            subject == null -> Refusal.UNAUTHENTICATED
            permits(subject, operation) -> null
            else -> Refusal.DENIED
        }

    /** A core session, or the subject a replaced session filter put on the request (Е3). */
    private fun authenticated(request: HttpServletRequest): Boolean =
        request.getAttribute(SESSION_REQUEST_ATTRIBUTE) is AdminSession ||
            request.getAttribute(SUBJECT_REQUEST_ATTRIBUTE) is Subject

    /** The operation by its path template; null when there is none or it is exempt, so the policy is not asked. */
    private fun operationOf(request: HttpServletRequest): Operation? =
        (request.getAttribute(HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE) as? String)
            ?.let { Operation(request.method, it) }
            ?.takeUnless(::exempt)

    private fun exempt(operation: Operation) = operation.toString().let { it in PUBLIC_OPERATIONS || it in EXEMPT }

    private fun permits(
        subject: Subject,
        operation: Operation,
    ): Boolean =
        runCatching { policy.getObject().permits(subject, operation) }
            .getOrElse {
                log.warn("The access policy failed on {}, access is denied: {}", operation, it.message)
                false
            }

    private enum class Refusal(
        val status: Int,
        val title: String,
        val code: ErrorCode,
    ) {
        UNAUTHENTICATED(HTTP_UNAUTHORIZED, "Unauthorized", ErrorCode.UNAUTHENTICATED),
        DENIED(HTTP_FORBIDDEN, "Forbidden", ErrorCode.ACCESS_DENIED),
    }

    private companion object {
        val EXEMPT = WIZARD_OPERATIONS + SIGN_OUT
    }
}
