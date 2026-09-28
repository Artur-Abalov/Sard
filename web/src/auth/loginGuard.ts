// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { resolveRedirectTarget } from './redirect'
import { checkSession, UnauthenticatedError } from './session'

/**
 * Where /login's beforeLoad should send an already signed-in visitor (Р9б), or null
 * to let the login page open (no session, or the check itself failed — Р9в: a failed
 * check is not the same as "not signed in", so the login page opens as usual and the
 * form reports the server being unavailable when submitted).
 */
export async function redirectIfSignedIn(
  redirectParam: string | undefined,
): Promise<string | null> {
  try {
    await checkSession()
  } catch (error) {
    if (error instanceof UnauthenticatedError) return null
    return null
  }
  return resolveRedirectTarget(redirectParam)
}
