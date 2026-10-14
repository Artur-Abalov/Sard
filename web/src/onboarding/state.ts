// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../api/schema'

export type Onboarding = components['schemas']['Onboarding']
type StepId = components['schemas']['OnboardingStepId']

/** True while nobody has set the administrator password: the wizard is the only way in (Рк1). */
export function adminPending(onboarding: Onboarding): boolean {
  return onboarding.steps.some((step) => step.id === 'admin' && step.state === 'pending')
}

/** True while the server refuses every change but the self-backup's: only the wizard is useful (Рк9). */
export function restrictsConsole(onboarding: Onboarding): boolean {
  return onboarding.restriction === 'full'
}

// The wizard screen the server's answer calls for. Which one is decided from the answer
// alone: the console keeps no state of its own about the wizard (Рк3).
export type Screen = 'code' | 'restart' | 'ca' | 'admin' | 'selfBackup' | 'keys' | 'complete'

function stateOf(onboarding: Onboarding, id: StepId) {
  return onboarding.steps.find((step) => step.id === id)?.state
}

// With an administrator session the server's steps lead (Рк10): the self-backup while it is
// pending, the key copy once the server reports the self-backup done, nothing when both are.
function afterAdmin(onboarding: Onboarding): Screen {
  if (stateOf(onboarding, 'self_backup') === 'pending') return 'selfBackup'
  return stateOf(onboarding, 'keys_confirmed') === 'pending' ? 'keys' : 'complete'
}

export function screenOf(onboarding: Onboarding): Screen {
  if (onboarding.access === 'none') {
    return onboarding.setupCode === 'active' ? 'code' : 'restart'
  }
  if (stateOf(onboarding, 'ca') !== 'done') return 'ca'
  if (stateOf(onboarding, 'admin') === 'done' && onboarding.access === 'admin') {
    return afterAdmin(onboarding)
  }
  return 'admin'
}

export interface StepMark {
  id: StepId
  // all four steps are required: no step is only announced (Рк5, revised by F4b)
  mark: 'done' | 'current' | 'todo'
}

export function stepMarks(onboarding: Onboarding): StepMark[] {
  let currentTaken = false
  return onboarding.steps.map(({ id, state }) => {
    if (state === 'done') return { id, mark: 'done' }
    if (state === 'upcoming') return { id, mark: 'todo' }
    const mark = currentTaken ? 'todo' : 'current'
    currentTaken = true
    return { id, mark }
  })
}

/** Where the banner of an upgraded installation points (Рк16); null when there is nothing to say. */
export function onboardingBanner(onboarding: Onboarding): { to: '/setup' } | null {
  return onboarding.restriction === 'new_agents' ? { to: '/setup' } : null
}
