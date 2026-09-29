// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

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
