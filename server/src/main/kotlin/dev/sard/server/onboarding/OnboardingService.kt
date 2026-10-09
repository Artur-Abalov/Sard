// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.AdminStepResult
import dev.sard.server.api.CaInfo
import dev.sard.server.api.CodeResult
import dev.sard.server.api.NoSuchSessionException
import dev.sard.server.api.Onboarding
import dev.sard.server.api.OnboardingAccess
import dev.sard.server.api.OnboardingApi
import dev.sard.server.api.OnboardingStep
import dev.sard.server.api.OnboardingStepId
import dev.sard.server.api.OnboardingStepState
import dev.sard.server.api.SetupCodeState
import dev.sard.server.auth.AdminSetup
import dev.sard.server.auth.LoginAttemptTracker
import dev.sard.server.auth.PasswordRules
import dev.sard.server.auth.SessionStore
import dev.sard.server.extension.TenantResolver
import dev.sard.server.pki.CaUsage
import org.slf4j.LoggerFactory
import java.time.Clock

private val log = LoggerFactory.getLogger(OnboardingService::class.java)

/**
 * The first-start wizard (docs/specs/server/onboarding-setup.feature): the setup code, the setup sessions it
 * buys, the ca step and the admin step. Setup sessions and the code live in memory, so a restart ends them. The
 * database is read for every question about steps. Nothing here logs a code, a password or a session id.
 *
 * Code entry has its own failure counter per client address ([codeAttempts], Р4), independent of the sign-in one.
 */
class OnboardingService(
    private val clock: Clock,
    private val adminSetup: AdminSetup,
    private val steps: OnboardingSteps,
    private val codes: SetupCodes,
    private val setupSessions: SetupSessions,
    private val codeAttempts: LoginAttemptTracker,
    private val adminSessions: SessionStore,
    private val tenantResolver: TenantResolver,
    private val caUsage: () -> CaUsage,
    private val caInfo: () -> CaInfo,
) : OnboardingApi {
    override fun state(
        adminSession: Boolean,
        setupSessionId: String?,
    ): Onboarding {
        val access = accessOf(adminSession, setupSessionId)
        val seesCa = access != OnboardingAccess.NONE
        return Onboarding(
            steps = stepStates(),
            setupCode = if (adminSetup.done()) SetupCodeState.NOT_ISSUED else codes.state(),
            access = access,
            ca = if (seesCa) caInfo() else null,
            caReplaceable = if (seesCa) caUsage() == CaUsage.NONE else null,
        )
    }

    private fun accessOf(
        adminSession: Boolean,
        setupSessionId: String?,
    ) = when {
        adminSession -> OnboardingAccess.ADMIN
        setupSessions.valid(setupSessionId) -> OnboardingAccess.SETUP
        else -> OnboardingAccess.NONE
    }

    private fun stepStates(): List<OnboardingStep> =
        listOf(
            OnboardingStep(OnboardingStepId.CA, doneOrPending(steps.caConfirmed())),
            OnboardingStep(OnboardingStepId.ADMIN, doneOrPending(adminSetup.done())),
            OnboardingStep(OnboardingStepId.SELF_BACKUP, OnboardingStepState.UPCOMING),
            OnboardingStep(OnboardingStepId.KEYS_CONFIRMED, OnboardingStepState.UPCOMING),
        )

    private fun doneOrPending(done: Boolean) = if (done) OnboardingStepState.DONE else OnboardingStepState.PENDING

    /** Р5: the admin step first (not an attempt), then the lock of the address, then the code itself. */
    override fun enterCode(
        code: String?,
        clientAddress: String,
        previousSetupSessionId: String?,
    ): CodeResult =
        codeAttempts.withAddressLock(clientAddress) {
            val retryAfter = codeAttempts.retryAfterSeconds(clientAddress)
            when {
                adminSetup.done() -> CodeResult.Completed
                retryAfter != null -> CodeResult.Locked(retryAfter)
                codes.accepts(code) -> accepted(clientAddress, previousSetupSessionId)
                else -> rejected(clientAddress)
            }
        }

    private fun accepted(
        clientAddress: String,
        previousSetupSessionId: String?,
    ): CodeResult.Accepted {
        codeAttempts.recordSuccess(clientAddress)
        previousSetupSessionId?.let(setupSessions::end)
        val id = setupSessions.create(checkNotNull(codes.expiresAt()))
        log.info("Setup code accepted from {}", clientAddress)
        return CodeResult.Accepted(id)
    }

    private fun rejected(clientAddress: String): CodeResult.Rejected {
        if (codeAttempts.recordFailure(clientAddress)) log.warn("Setup code entry locked from {}", clientAddress)
        log.info("Setup code rejected from {}", clientAddress)
        return CodeResult.Rejected
    }

    override fun confirmCa(
        setupSessionId: String?,
        adminSession: Boolean,
        clientAddress: String,
    ) {
        // An extension that signs people in has no setup sessions: its administrator confirms (Р16).
        val allowed = setupSessions.valid(setupSessionId) || (adminSetup.external && adminSession)
        if (!allowed) throw NoSuchSessionException()
        if (steps.confirmCa(clock.instant())) {
            log.info("Onboarding step ca completed from {}: CA fingerprint={}", clientAddress, caInfo().fingerprint)
        }
    }

    override fun completeAdmin(
        setupSessionId: String?,
        password: String?,
        clientAddress: String,
    ): AdminStepResult {
        // With an external sign-in there is nothing to set up, and no setup session to ask for.
        if (adminSetup.external) return AdminStepResult.Completed
        if (!setupSessions.valid(setupSessionId)) throw NoSuchSessionException()
        return when {
            !steps.caConfirmed() -> AdminStepResult.CaPending
            !acceptable(password) -> AdminStepResult.InvalidPassword
            adminSetup.create(checkNotNull(password)) -> adminDone(clientAddress)
            else -> AdminStepResult.Completed
        }
    }

    private fun acceptable(password: String?): Boolean = password != null && PasswordRules.acceptable(password)

    private fun adminDone(clientAddress: String): AdminStepResult.Done {
        codes.close()
        setupSessions.endAll()
        log.info("Onboarding step admin completed from {}", clientAddress)
        return AdminStepResult.Done(adminSessions.create(tenantResolver.currentTenantId()).id)
    }
}
