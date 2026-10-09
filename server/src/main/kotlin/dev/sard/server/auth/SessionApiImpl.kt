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
            // A locked address does not even reach the database.
            val stored = if (retryAfter == null) administrators.hash() else null
            when {
                retryAfter != null -> SignInResult.Locked(retryAfter)
                stored == null -> SignInResult.SetupRequired
                hasher.matches(password, stored) -> signIn(clientAddress, previousSessionId)
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

    /** Р9: the rules of the fields first (not an attempt), then the lock of the address, then the current password. */
    override fun changePassword(
        sessionId: String,
        currentPassword: String?,
        newPassword: String?,
        clientAddress: String,
    ): PasswordChangeResult =
        when {
            currentPassword == null -> {
                PasswordChangeResult.InvalidField("currentPassword")
            }

            newPassword == null || !PasswordRules.acceptable(newPassword) -> {
                PasswordChangeResult.InvalidField("newPassword")
            }

            else -> {
                attempt(sessionId, currentPassword, newPassword, clientAddress)
            }
        }

    private fun attempt(
        sessionId: String,
        current: String,
        new: String,
        clientAddress: String,
    ): PasswordChangeResult =
        attemptTracker.withAddressLock(clientAddress) {
            val retryAfter = attemptTracker.retryAfterSeconds(clientAddress)
            if (retryAfter == null) {
                replace(sessionId, current, new, clientAddress)
            } else {
                PasswordChangeResult.Locked(retryAfter)
            }
        }

    private fun replace(
        sessionId: String,
        current: String,
        new: String,
        clientAddress: String,
    ): PasswordChangeResult {
        if (!swapped(current, new)) return failedChange(clientAddress)
        attemptTracker.recordSuccess(clientAddress)
        val tenantId = sessionStore.find(sessionId)?.tenantId ?: tenantResolver.currentTenantId()
        sessionStore.removeAll()
        log.info("Password changed from {}", clientAddress)
        return PasswordChangeResult.Changed(sessionStore.create(tenantId).id)
    }

    /** True when [current] is the password and the hash of [new] took its place, unless another change came first. */
    private fun swapped(
        current: String,
        new: String,
    ): Boolean {
        val stored = administrators.hash() ?: return false
        return hasher.matches(current, stored) && administrators.replaceHash(stored, hasher.hash(new), clock.instant())
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
