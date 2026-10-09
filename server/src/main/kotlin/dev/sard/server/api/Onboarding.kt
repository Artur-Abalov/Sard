// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import com.fasterxml.jackson.annotation.JsonProperty
import io.swagger.v3.oas.annotations.media.Schema

/** Name of the setup session cookie (F4a, Р3). */
const val SETUP_COOKIE = "sard_setup"

/** The only path the setup cookie travels on. */
const val SETUP_COOKIE_PATH = "/api/v1/onboarding"

@Schema(enumAsRef = true, description = "A step of the first-start wizard, in the order of the wizard")
enum class OnboardingStepId {
    @JsonProperty("ca")
    CA,

    @JsonProperty("admin")
    ADMIN,

    @JsonProperty("self_backup")
    SELF_BACKUP,

    @JsonProperty("keys_confirmed")
    KEYS_CONFIRMED,
}

@Schema(enumAsRef = true, description = "upcoming: the step is not part of this version of the wizard yet")
enum class OnboardingStepState {
    @JsonProperty("done")
    DONE,

    @JsonProperty("pending")
    PENDING,

    @JsonProperty("upcoming")
    UPCOMING,
}

@Schema(
    enumAsRef = true,
    description =
        "active: a code was issued at this start and has not expired; expired: it was issued and its time " +
            "passed; not_issued: there is none (the admin step is done, or was opened by admin-reset after the start)",
)
enum class SetupCodeState {
    @JsonProperty("active")
    ACTIVE,

    @JsonProperty("expired")
    EXPIRED,

    @JsonProperty("not_issued")
    NOT_ISSUED,
}

@Schema(enumAsRef = true, description = "Which session came with the request")
enum class OnboardingAccess {
    @JsonProperty("none")
    NONE,

    @JsonProperty("setup")
    SETUP,

    @JsonProperty("admin")
    ADMIN,
}

@Schema(
    enumAsRef = true,
    description = "Where the CA of the server came from; unknown for a CA created before the server recorded it",
)
enum class CaOrigin {
    @JsonProperty("generated")
    GENERATED,

    @JsonProperty("imported")
    IMPORTED,

    @JsonProperty("unknown")
    UNKNOWN,
}

@Schema(description = "A step of the wizard and where it stands")
data class OnboardingStep(
    val id: OnboardingStepId,
    val state: OnboardingStepState,
)

@Schema(description = "How far the first start has come; public, the CA only with a setup or administrator session")
data class Onboarding(
    @field:Schema(description = "Exactly ca, admin, self_backup, keys_confirmed, in this order")
    val steps: List<OnboardingStep>,
    val setupCode: SetupCodeState,
    val access: OnboardingAccess,
    @field:Schema(description = "The CA of the server; null without a session")
    val ca: CaInfo?,
    @field:Schema(
        description =
            "True while the CA may still be replaced by an import: the ca step is not done and no agent " +
                "certificate was issued; null without a session",
    )
    val caReplaceable: Boolean?,
)

@Schema(description = "The setup code printed in the server's log")
data class SetupCodeRequest(
    @field:Schema(description = "As printed, or as typed: case, hyphens and spaces do not matter")
    val code: String? = null,
)

@Schema(description = "The administrator password to set; 12 to 1024 Unicode code points")
data class AdminStepRequest(
    val password: String? = null,
)

@Schema(description = "A password change; the new password is 12 to 1024 Unicode code points")
data class PasswordChangeRequest(
    val currentPassword: String? = null,
    val newPassword: String? = null,
)

/** The outcome of entering the setup code. */
sealed interface CodeResult {
    data class Accepted(
        val setupSessionId: String,
    ) : CodeResult

    data object Rejected : CodeResult

    data class Locked(
        val retryAfterSeconds: Long,
    ) : CodeResult

    /** The admin step is done, so there is nothing to enter a code for. */
    data object Completed : CodeResult
}

/** The outcome of the admin step. */
sealed interface AdminStepResult {
    data class Done(
        val sessionId: String,
    ) : AdminStepResult

    data object CaPending : AdminStepResult

    data object Completed : AdminStepResult

    data object InvalidPassword : AdminStepResult
}

/** The outcome of a password change. */
sealed interface PasswordChangeResult {
    data class Changed(
        val sessionId: String,
    ) : PasswordChangeResult

    data object WrongPassword : PasswordChangeResult

    data class InvalidField(
        val field: String,
    ) : PasswordChangeResult

    data class Locked(
        val retryAfterSeconds: Long,
    ) : PasswordChangeResult

    /** The session is managed elsewhere (an enterprise SessionApi): 501. */
    data object NotSupported : PasswordChangeResult
}

/**
 * The first-start wizard (F4a): a domain port with no HTTP types, like [SessionApi]. Setup sessions are checked by the
 * filter before a call reaches it; [setupSessionId] is passed where the answer depends on it.
 */
interface OnboardingApi {
    /** [adminSessionId] and [setupSessionId] are the ids the request carried, if any. */
    fun state(
        adminSessionId: String?,
        setupSessionId: String?,
    ): Onboarding

    fun enterCode(
        code: String?,
        clientAddress: String,
        previousSetupSessionId: String?,
    ): CodeResult

    fun confirmCa(clientAddress: String)

    fun completeAdmin(
        password: String?,
        clientAddress: String,
    ): AdminStepResult
}
