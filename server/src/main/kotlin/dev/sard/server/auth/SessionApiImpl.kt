// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.NoSuchSessionException
import dev.sard.server.api.SessionApi
import dev.sard.server.api.SignInResult
import dev.sard.server.extension.TenantResolver
import org.slf4j.LoggerFactory
import dev.sard.server.api.Session as SessionResponse

private val log = LoggerFactory.getLogger(SessionApiImpl::class.java)

/**
 * Sign-in, current session and sign-out (D2, W1b): pure domain logic, no HTTP types
 * (an enterprise starter can implement [SessionApi] instead, e.g. with SSO, without a
 * dependency on this module's web layer). [dev.sard.server.api.SessionController] maps
 * [SignInResult] and [NoSuchSessionException] to status, cookies and body.
 */
class SessionApiImpl(
    private val passwordAuthenticator: AdminPasswordAuthenticator,
    private val sessionStore: SessionStore,
    private val attemptTracker: LoginAttemptTracker,
    private val tenantResolver: TenantResolver,
) : SessionApi {
    override fun createSession(
        password: String,
        clientAddress: String,
        previousSessionId: String?,
    ): SignInResult =
        // The whole attempt is one atomic step per address (see withAddressLock): otherwise
        // concurrent requests could all read "not locked" before any of them is recorded.
        attemptTracker.withAddressLock(clientAddress) {
            val retryAfter = attemptTracker.retryAfterSeconds(clientAddress)
            when {
                retryAfter != null -> SignInResult.Locked(retryAfter)
                passwordAuthenticator.matches(password) -> signIn(clientAddress, previousSessionId)
                else -> wrongPassword(clientAddress)
            }
        }

    override fun getSession(sessionId: String): SessionResponse {
        // Not touch(): SessionAuthFilter already touched this same session earlier in the
        // request (every guarded route does, Р1) — touching again here would be redundant.
        val session = sessionStore.find(sessionId) ?: throw NoSuchSessionException()
        return SessionResponse(session.tenantId, sessionStore.expiresAt(session))
    }

    override fun deleteSession(
        sessionId: String,
        clientAddress: String,
    ) {
        if (!sessionStore.remove(sessionId)) throw NoSuchSessionException()
        log.info("Signed out from {}", clientAddress)
    }

    private fun signIn(
        clientAddress: String,
        previousSessionId: String?,
    ): SignInResult.SignedIn {
        attemptTracker.recordSuccess(clientAddress)
        previousSessionId?.let { sessionStore.remove(it) }
        val session = sessionStore.create(tenantResolver.currentTenantId())
        log.info("Sign-in succeeded from {}", clientAddress)
        return SignInResult.SignedIn(session.id)
    }

    private fun wrongPassword(clientAddress: String): SignInResult.WrongPassword {
        val justLocked = attemptTracker.recordFailure(clientAddress)
        if (justLocked) log.warn("Sign-in locked from {}", clientAddress)
        log.info("Sign-in failed from {}", clientAddress)
        return SignInResult.WrongPassword
    }
}
