// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.CaInfo
import dev.sard.server.api.CaOrigin
import dev.sard.server.api.CodeResult
import dev.sard.server.auth.AdminSetup
import dev.sard.server.auth.LoginAttemptTracker
import dev.sard.server.auth.SessionStore
import dev.sard.server.pki.CaUsage
import dev.sard.server.pki.MovableClock
import java.time.Instant
import java.util.UUID

val T0: Instant = Instant.parse("2026-10-09T12:00:00Z")
const val CODE = "ABCD-EFGH-JKMN-PQRS-TVWX-YZ01-2345"
const val WRONG_CODE = "0000-0000-0000-0000-0000-0000-0000"
const val ADDRESS = "203.0.113.10"
const val FINGERPRINT = "8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f"

/** The admin step in memory. */
open class FakeAdminSetup(
    override val external: Boolean = false,
    var password: String? = null,
) : AdminSetup {
    override fun done(): Boolean = external || password != null

    override fun create(password: String): Boolean {
        if (done()) return false
        this.password = password
        return true
    }
}

class FakeOnboardingSteps(
    var ca: Boolean = false,
) : OnboardingSteps {
    override fun caConfirmed() = ca

    override fun confirmCa(now: Instant): Boolean = !ca.also { ca = true }
}

/** A service with everything in memory; the code [CODE] is issued when the harness is built unless told otherwise. */
class OnboardingHarness(
    val clock: MovableClock = MovableClock(T0),
    val adminSetup: FakeAdminSetup = FakeAdminSetup(),
    val steps: FakeOnboardingSteps = FakeOnboardingSteps(),
    var usage: CaUsage = CaUsage.NONE,
    issueCode: Boolean = true,
) {
    val codes = SetupCodes(clock) { CODE }
    val sessions = SetupSessions(clock)
    val adminSessions = SessionStore(clock)
    val attempts = LoginAttemptTracker(clock)
    val tenant: UUID = UUID.fromString("00000000-0000-0000-0000-000000000001")
    val caInfo = CaInfo(FINGERPRINT, CaOrigin.GENERATED, "/var/lib/sard/pki/ca/ca.key")
    val service =
        OnboardingService(
            clock,
            adminSetup,
            steps,
            codes,
            sessions,
            attempts,
            adminSessions,
            { tenant },
            { usage },
            { caInfo },
        )

    init {
        if (issueCode) codes.issue()
    }

    /** Enters the right code and returns the setup session. */
    fun setupSession(): String {
        val entered = service.enterCode(CODE, ADDRESS, null) as CodeResult.Accepted
        return entered.setupSessionId
    }
}
