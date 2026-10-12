// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.OnboardingRestriction
import dev.sard.server.api.OnboardingStep
import dev.sard.server.api.OnboardingStepId
import dev.sard.server.api.OnboardingStepState

private fun doneOrPending(done: Boolean) = if (done) OnboardingStepState.DONE else OnboardingStepState.PENDING

private fun selfBackupState(
    adminDone: Boolean,
    selfBackupDone: Boolean,
) = when {
    selfBackupDone -> OnboardingStepState.DONE
    adminDone -> OnboardingStepState.PENDING
    else -> OnboardingStepState.UPCOMING
}

private fun keysState(
    complete: Boolean,
    selfBackupDone: Boolean,
) = when {
    complete -> OnboardingStepState.DONE
    selfBackupDone -> OnboardingStepState.PENDING
    else -> OnboardingStepState.UPCOMING
}

/**
 * Р20: the self-backup step waits for the admin step and is done with the first backups of the binding; the key copy
 * waits for the self-backup. A [complete] onboarding stays complete: nothing that happens later takes a step back.
 * [firstBackupDone] is asked only when the answer can matter.
 */
internal fun stepsOf(
    caDone: Boolean,
    adminDone: Boolean,
    complete: Boolean,
    firstBackupDone: () -> Boolean,
): List<OnboardingStep> {
    val selfBackupDone = complete || (adminDone && firstBackupDone())
    return listOf(
        OnboardingStep(OnboardingStepId.CA, doneOrPending(caDone)),
        OnboardingStep(OnboardingStepId.ADMIN, doneOrPending(adminDone)),
        OnboardingStep(OnboardingStepId.SELF_BACKUP, selfBackupState(adminDone, selfBackupDone)),
        OnboardingStep(OnboardingStepId.KEYS_CONFIRMED, keysState(complete, selfBackupDone)),
    )
}

/** Р21: none once the key copy is confirmed; before that new_agents on an upgraded installation, else full. */
internal fun restrictionOf(
    complete: Boolean,
    upgraded: Boolean,
): OnboardingRestriction =
    when {
        complete -> OnboardingRestriction.NONE
        upgraded -> OnboardingRestriction.NEW_AGENTS
        else -> OnboardingRestriction.FULL
    }
