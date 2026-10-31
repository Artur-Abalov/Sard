// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.SESSION_COOKIE
import dev.sard.server.api.SESSION_REQUEST_ATTRIBUTE
import dev.sard.server.extension.SubjectAuthentication
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.springframework.security.core.context.DeferredSecurityContext
import org.springframework.security.core.context.SecurityContext
import org.springframework.security.core.context.SecurityContextHolder
import org.springframework.security.web.context.HttpRequestResponseHolder
import org.springframework.security.web.context.SecurityContextRepository

/**
 * The session of the core as the security context of a request (CORE1 Р36): the store of sessions behind the
 * cookie [SESSION_COOKIE] stands in for an HttpSession, which the server never creates. A valid cookie gives a
 * [SubjectAuthentication] of the session's subject (the session itself is attached to the request for the code of the
 * core that needs its tenant) and extends the session (Р1, Р52); the first cookie of that name
 * in the header is the one that counts (Р53). Only requests under /api/v1 are looked at, and not the
 * [SESSIONLESS_OPERATIONS] among them. The context is read when the request enters the chain, after the Origin check,
 * and is never saved: it lives for one request (Р48).
 */
class SessionSecurityContextRepository(
    private val sessions: SessionStore,
) : SecurityContextRepository {
    override fun loadDeferredContext(request: HttpServletRequest): DeferredSecurityContext {
        val context = contextOf(request)
        return Resolved(context)
    }

    override fun loadContext(requestResponseHolder: HttpRequestResponseHolder): SecurityContext {
        val request = requestResponseHolder.request
        return contextOf(request)
    }

    override fun saveContext(
        context: SecurityContext,
        request: HttpServletRequest,
        response: HttpServletResponse,
    ) = Unit

    override fun containsContext(request: HttpServletRequest): Boolean = false

    private fun contextOf(request: HttpServletRequest): SecurityContext {
        val context = SecurityContextHolder.createEmptyContext()
        sessionOf(request)?.let {
            context.authentication = SubjectAuthentication(it.subject)
            request.setAttribute(SESSION_REQUEST_ATTRIBUTE, it)
        }
        return context
    }

    /** The session the request carries, touched; null when it carries none, a stale one, or is not looked at. */
    private fun sessionOf(request: HttpServletRequest): AdminSession? =
        when {
            !isApiPath(request.requestURI) -> null
            "${request.method} ${request.requestURI}" in SESSIONLESS_OPERATIONS -> null
            else -> firstSessionCookie(request)?.let { sessions.touch(it, clientAddress(request)) }
        }

    private fun firstSessionCookie(request: HttpServletRequest): String? =
        request.cookies
            .orEmpty()
            .firstOrNull { it.name == SESSION_COOKIE }
            ?.value

    private class Resolved(
        private val context: SecurityContext,
    ) : DeferredSecurityContext {
        override fun get(): SecurityContext = context

        override fun isGenerated(): Boolean = false
    }
}
