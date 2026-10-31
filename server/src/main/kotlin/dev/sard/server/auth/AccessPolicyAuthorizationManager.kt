// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.AccessPolicy
import dev.sard.server.extension.Operation
import dev.sard.server.extension.Subject
import dev.sard.server.extension.SubjectSource
import jakarta.servlet.http.HttpServletRequest
import org.slf4j.LoggerFactory
import org.springframework.beans.factory.ObjectProvider
import org.springframework.security.authentication.AnonymousAuthenticationToken
import org.springframework.security.authentication.InsufficientAuthenticationException
import org.springframework.security.authorization.AuthorizationDecision
import org.springframework.security.authorization.AuthorizationManager
import org.springframework.security.authorization.AuthorizationResult
import org.springframework.security.core.Authentication
import org.springframework.security.web.access.intercept.RequestAuthorizationContext
import java.util.function.Supplier

private val log = LoggerFactory.getLogger(AccessPolicyAuthorizationManager::class.java)
private val GRANTED = AuthorizationDecision(true)
private val DENIED = AuthorizationDecision(false)

/**
 * The authentication a request must carry to be let past the chain, or the 401 that the entry point of the chain
 * answers: anything but an authenticated, non-anonymous authentication is a missing one (Р38, Р39).
 */
internal fun requireAuthentication(authentication: Supplier<out Authentication?>) {
    val current = authentication.get()
    if (current == null || current is AnonymousAuthenticationToken || !current.isAuthenticated) {
        throw InsufficientAuthenticationException("Authentication is required")
    }
}

/** Sign-out (Р17): needs an authentication, is not subject to the access policy. */
internal class AuthenticatedAuthorizationManager : AuthorizationManager<RequestAuthorizationContext> {
    override fun authorize(
        authentication: Supplier<out Authentication?>,
        context: RequestAuthorizationContext,
    ): AuthorizationResult {
        requireAuthentication(authentication)
        return GRANTED
    }
}

/** The operation a request is, as OpenAPI names it; null when no handler of the API takes it. */
fun interface OperationResolver {
    fun operationOf(request: HttpServletRequest): Operation?
}

/**
 * The [AccessPolicy] as the authorization manager of the chain (CORE1 Р40), after the Origin check and the
 * authentication. A request without an authentication is refused with 401 whatever the [SubjectSource] would
 * say, and so is one whose authentication it names no subject for (Р30, Р39); the subject the policy is asked about
 * is the source's; a source that throws is 403 access_denied with one WARN (Р31). A request with no operation (no
 * handler, a method without an operation) is not asked: MVC answers it as it does without extensions. A policy
 * that throws refuses: the failure is closed (Р16). The beans come through providers: a configuration that needs
 * them directly makes Spring create them while the security configuration is built, before their post-processing.
 */
class AccessPolicyAuthorizationManager(
    private val policy: ObjectProvider<AccessPolicy>,
    private val subjects: ObjectProvider<SubjectSource>,
    private val operations: OperationResolver,
) : AuthorizationManager<RequestAuthorizationContext> {
    override fun authorize(
        authentication: Supplier<out Authentication?>,
        context: RequestAuthorizationContext,
    ): AuthorizationResult {
        requireAuthentication(authentication)
        val operation = operations.operationOf(context.request) ?: return GRANTED
        return runCatching { subjects.getObject().current() }.fold(
            onSuccess = { subject -> decisionFor(subject, operation) },
            onFailure = {
                log.warn("The subject source failed on {}, access is denied: {}", operation, it.message)
                DENIED
            },
        )
    }

    private fun decisionFor(
        subject: Subject?,
        operation: Operation,
    ): AuthorizationResult {
        if (subject == null) throw InsufficientAuthenticationException("The authentication names no subject")
        return if (permits(subject, operation)) GRANTED else DENIED
    }

    private fun permits(
        subject: Subject,
        operation: Operation,
    ): Boolean =
        runCatching { policy.getObject().permits(subject, operation) }
            .getOrElse {
                log.warn("The access policy failed on {}, access is denied: {}", operation, it.message)
                false
            }
}
