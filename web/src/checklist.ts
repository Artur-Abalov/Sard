// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'

type FirstSteps = components['schemas']['FirstSteps']
type Step = Exclude<keyof FirstSteps, 'complete'>

// Where an unfinished step leads; hints are explained on the page it opens.
export type Target =
  | { to: '/tokens'; search: { create: true } | { hint: 'enroll' } }
  | { to: '/agents'; search: { hint: 'repo-init' } }
  | { to: '/sources/new' }
  | { to: '/sources'; search: { hint: 'run-backup' } }

export interface ChecklistItem {
  step: Step
  done: boolean
  target: Target
}

const TARGETS: [Step, Target][] = [
  ['tokenIssued', { to: '/tokens', search: { create: true } }],
  ['agentConnected', { to: '/tokens', search: { hint: 'enroll' } }],
  ['repositoryInitialized', { to: '/agents', search: { hint: 'repo-init' } }],
  ['sourceCreated', { to: '/sources/new' }],
  ['backupSucceeded', { to: '/sources', search: { hint: 'run-backup' } }],
]

// The checklist of the overview from the marks the server sent, or null once the
// server says it is complete. Nothing is derived from other data (OB3).
export function buildChecklist(steps: FirstSteps): ChecklistItem[] | null {
  if (steps.complete) return null
  return TARGETS.map(([step, target]) => ({ step, done: steps[step], target }))
}
