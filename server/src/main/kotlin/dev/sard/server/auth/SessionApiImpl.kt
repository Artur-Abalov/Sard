// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.NoSuchSessionException
import dev.sard.server.api.PasswordChangeResult
import dev.sard.server.api.SessionApi
import dev.sard.server.api.SignInResult
import dev.sard.server.extension.TenantResolver
import org.slf4j.LoggerFactory
import java.time.Clock
import dev.sard.server.api.Session as SessionResponse

private val log = LoggerFactory.getLogger(SessionApiImpl::class.java)

/**
 * Sign-in, current session, sign-out and password change (D2, W1b, F4a): pure domain logic, no HTTP types
 * (an enterprise starter can implement [SessionApi] instead, e.g. with SSO, without a
 * dependency on this module's web layer). [dev.sard.server.api.SessionController] maps
 * [SignInResult] and [NoSuchSessionException] to status, cookies and body.
 *
 * The password is only ever a hash in the database, read for every check; a database that cannot be reached
 * throws a data access exception (503), which is no attempt and is not counted.
 */
class SessionApiImpl(
    private val administrators: Administrators,
    private val hasher: PasswordHasher,
    private val sessionStore: SessionStore,
    private val attemptTracker: LoginAttemptTracker,
    private val tenantResolver: TenantResolver,
    private val clock: Clock,
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
            if (retryAfter != null) return@withAddressLock SignInResult.Locked(retryAfter)
            val stored = administrators.hash() ?: return@withAddressLock SignInResult.SetupRequired
            if (hasher.matches(password, stored)) signIn(clientAddress, previousSessionId) else wrongPassword(clientAddress)
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

    /** Р9: the rules of the fields first (not an attempt), then the lock of the address, then the current password. */
    override fun changePassword(
        sessionId: String,
        currentPassword: String?,
        newPassword: String?,
        clientAddress: String,
    ): PasswordChangeResult {
        val current = currentPassword ?: return PasswordChangeResult.InvalidField("currentPassword")
        val new = newPassword?.takeIf(PasswordRules::acceptable) ?: return PasswordChangeResult.InvalidField("newPassword")
        return attemptTracker.withAddressLock(clientAddress) {
            val retryAfter = attemptTracker.retryAfterSeconds(clientAddress)
            if (retryAfter != null) PasswordChangeResult.Locked(retryAfter) else replace(sessionId, current, new, clientAddress)
        }
    }

    private fun replace(
        sessionId: String,
        current: String,
        new: String,
        clientAddress: String,
    ): PasswordChangeResult {
        val stored = administrators.hash()
        val replaced =
            stored != null &&
                hasher.matches(current, stored) &&
                administrators.replaceHash(stored, hasher.hash(new), clock.instant())
        if (!replaced) return failedChange(clientAddress)
        attemptTracker.recordSuccess(clientAddress)
        val tenantId = sessionStore.find(sessionId)?.tenantId ?: tenantResolver.currentTenantId()
        sessionStore.removeAll()
        log.info("Password changed from {}", clientAddress)
        return PasswordChangeResult.Changed(sessionStore.create(tenantId).id)
    }

    private fun failedChange(clientAddress: String): PasswordChangeResult {
        lockWarning(attemptTracker.recordFailure(clientAddress), clientAddress)
        log.info("Password change failed from {}", clientAddress)
        return PasswordChangeResult.WrongPassword
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
        lockWarning(attemptTracker.recordFailure(clientAddress), clientAddress)
        log.info("Sign-in failed from {}", clientAddress)
        return SignInResult.WrongPassword
    }

    private fun lockWarning(
        justLocked: Boolean,
        clientAddress: String,
    ) {
        if (justLocked) log.warn("Sign-in locked from {}", clientAddress)
    }
}
