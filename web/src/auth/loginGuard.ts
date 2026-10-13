// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { fetchOnboarding } from '../onboarding/api'
import { adminPending } from '../onboarding/state'
import { resolveRedirectTarget } from './redirect'
import { checkSession } from './session'

/**
 * Where /login's beforeLoad should send an already signed-in visitor (Р9б), or null
 * to let the login page open. Any failed check — no session (401) or the check itself
 * failing (Р9в) — is treated the same here: a failed check is not the same as "signed
 * in", so the login page opens as usual and the form reports the server being
 * unavailable when submitted.
 */
export async function redirectIfSignedIn(
  redirectParam: string | undefined,
): Promise<string | null> {
  try {
    await checkSession()
  } catch {
    return null
  }
  return resolveRedirectTarget(redirectParam)
}

// Whether the admin step is pending, or null when the state cannot be read. An unreadable
// state is neither "pending" nor "finished": the page opens as usual and reports the failure itself.
async function adminStepPending(): Promise<boolean | null> {
  try {
    return adminPending(await fetchOnboarding())
  } catch {
    return null
  }
}

// Whether the visitor must go to the wizard first. A state that cannot be read is not
// "pending": the login page opens as usual and its form reports a failure on submit.
export async function setupPending(): Promise<boolean> {
  return (await adminStepPending()) === true
}

// Whether the wizard has nothing left to do for this visitor: the admin step is done.
// A state that cannot be read leaves the wizard open: it shows the failure itself.
export async function setupFinished(): Promise<boolean> {
  return (await adminStepPending()) === false
}
