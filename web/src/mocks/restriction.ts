// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { problem } from './problems'
import { state } from './state'

export type Restriction = 'full' | 'new_agents' | 'none'

/** What the mock refuses now (Р21): none once the key copy is confirmed, else by the kind of installation. */
export function restriction(): Restriction {
  if (state.onboarding.keysDone) return 'none'
  return state.onboarding.upgraded ? 'new_agents' : 'full'
}

export const onboardingIncomplete = problem(409, 'Conflict', 'onboarding_incomplete')

/**
 * Whether the mock refuses a change (D8, Р22): [tokens] is the issue of an enrollment token, which an upgraded
 * installation refuses too; [exempt] is a change the full restriction lets through (a system source's schedule,
 * the built-in agent's revocation). Runs after the session check and before the body is read, as on the server.
 */
export function refusedUntilComplete(tokens = false, exempt = false): boolean {
  const current = restriction()
  if (current === 'none') return false
  if (tokens) return true
  return current === 'full' && !exempt
}
