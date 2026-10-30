// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.ErrorCode
import dev.sard.server.api.SESSION_REQUEST_ATTRIBUTE
import dev.sard.server.api.writeProblem
import dev.sard.server.extension.AccessPolicy
import dev.sard.server.extension.Operation
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.slf4j.LoggerFactory
import org.springframework.beans.factory.ObjectProvider
import org.springframework.web.servlet.HandlerInterceptor
import org.springframework.web.servlet.HandlerMapping
import tools.jackson.databind.ObjectMapper

private val log = LoggerFactory.getLogger(AccessPolicyInterceptor::class.java)
private const val HTTP_FORBIDDEN = 403
private const val SIGN_OUT = "DELETE /api/v1/session"

/**
 * Asks the [AccessPolicy] after the session (401) and the Origin (403) have been checked, for a request with an
 * administrator session. It is an interceptor, not a filter, because the operation is named by its path template,
 * which the handler mapping finds. The public operations, the wizard's and sign-out are not subject to it (Р17).
 * A policy that throws refuses: the failure is closed (Р16). The beans come through providers: a configurer that
 * needs them directly makes Spring create them while the MVC configuration is built, before their post-processing.
 */
class AccessPolicyInterceptor(
    private val policy: ObjectProvider<AccessPolicy>,
    private val objectMapper: ObjectProvider<ObjectMapper>,
) : HandlerInterceptor {
    override fun preHandle(
        request: HttpServletRequest,
        response: HttpServletResponse,
        handler: Any,
    ): Boolean {
        val allowed = askedAbout(request)?.let { (session, operation) -> permits(session, operation) } ?: true
        if (!allowed) {
            writeProblem(response, objectMapper.getObject(), HTTP_FORBIDDEN, "Forbidden", ErrorCode.ACCESS_DENIED)
        }
        return allowed
    }

    /** The session and the operation the policy is asked about; null when it is not asked (Р17). */
    private fun askedAbout(request: HttpServletRequest): Pair<AdminSession, Operation>? {
        val session = request.getAttribute(SESSION_REQUEST_ATTRIBUTE) as? AdminSession
        val template = request.getAttribute(HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE) as? String
        val operation = template?.let { Operation(request.method, it) }?.takeUnless(::exempt)
        return if (session == null || operation == null) null else session to operation
    }

    private fun exempt(operation: Operation) = operation.toString().let { it in PUBLIC_OPERATIONS || it in EXEMPT }

    private fun permits(
        session: AdminSession,
        operation: Operation,
    ): Boolean =
        runCatching { policy.getObject().permits(session.subject, operation) }
            .getOrElse {
                log.warn("The access policy failed on {}, access is denied: {}", operation, it.message)
                false
            }

    private companion object {
        val EXEMPT = WIZARD_OPERATIONS + SIGN_OUT
    }
}
