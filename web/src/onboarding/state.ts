// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../api/schema'

export type Onboarding = components['schemas']['Onboarding']
type StepId = components['schemas']['OnboardingStepId']

/** True while nobody has set the administrator password: the wizard is the only way in (Рк1). */
export function adminPending(onboarding: Onboarding): boolean {
  return onboarding.steps.some((step) => step.id === 'admin' && step.state === 'pending')
}

// The wizard screen the server's answer calls for. Which one is decided from the answer
// alone: the console keeps no state of its own about the wizard (Рк3).
export type Screen = 'code' | 'restart' | 'ca' | 'admin'

export function screenOf(onboarding: Onboarding): Screen {
  if (onboarding.access === 'none') {
    return onboarding.setupCode === 'active' ? 'code' : 'restart'
  }
  const ca = onboarding.steps.find((step) => step.id === 'ca')
  return ca?.state === 'done' ? 'admin' : 'ca'
}

export interface StepMark {
  id: StepId
  // upcoming steps of F4b are only announced: no action, no sign of being required (Рк5)
  mark: 'done' | 'current' | 'todo' | 'soon'
}

export function stepMarks(onboarding: Onboarding): StepMark[] {
  let currentTaken = false
  return onboarding.steps.map(({ id, state }) => {
    if (state === 'done') return { id, mark: 'done' }
    if (state === 'upcoming') return { id, mark: 'soon' }
    const mark = currentTaken ? 'todo' : 'current'
    currentTaken = true
    return { id, mark }
  })
}
