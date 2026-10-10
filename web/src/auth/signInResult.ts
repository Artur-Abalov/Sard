// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { formatLockoutMinutes } from './lockout'

export type SignInResult =
  | { kind: 'ok' }
  // 409 setup_required: there is no administrator yet, the wizard is the way in
  | { kind: 'setup' }
  | { kind: 'invalid' }
  | { kind: 'locked'; minutes: number }
  | { kind: 'unavailable' }

/** What the sign-in form does with the answer of POST /api/v1/session. */
export function signInResult(status: number, retryAfter: string | null): SignInResult {
  switch (status) {
    case 204:
      return { kind: 'ok' }
    case 409:
      return { kind: 'setup' }
    case 401:
      return { kind: 'invalid' }
    case 429:
      return { kind: 'locked', minutes: formatLockoutMinutes(Number(retryAfter ?? '0')) }
    default:
      return { kind: 'unavailable' }
  }
}
