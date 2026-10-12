// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { fetchOnboarding } from '../onboarding/api'
import { adminPending, type Onboarding } from '../onboarding/state'
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

async function readOnboarding(): Promise<Onboarding | null> {
  try {
    return await fetchOnboarding()
  } catch {
    return null
  }
}

async function signedIn(): Promise<boolean> {
  try {
    await checkSession()
    return true
  } catch {
    return false
  }
}

/**
 * Where the wizard route sends a visit, or null to let it open (Рк2, Рк10). Before the admin step the
 * wizard is the way in. After it a visit without a session goes to sign-in, and a signed-in one goes home
 * once the onboarding is complete; while the server still restricts, the wizard shows the step it is at.
 * A state that cannot be read leaves the wizard open: it shows the failure itself.
 */
export async function setupRedirect(): Promise<'/login' | '/' | null> {
  const onboarding = await readOnboarding()
  if (onboarding === null || adminPending(onboarding)) return null
  if (!(await signedIn())) return '/login'
  return onboarding.restriction === 'none' ? '/' : null
}
