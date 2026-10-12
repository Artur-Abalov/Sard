// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../api/schema'
import { freshTracker, type Tracker } from './lockout'
import {
  agents,
  deletedSources,
  enrollmentTokens,
  runs,
  scheduleFires,
  schedules,
  snapshots,
  sources,
  MOCK_PASSWORD,
  stepLogs,
} from './fixtures'

type Schemas = components['schemas']

/** What the mock server remembers between requests; reset per test. */
export interface MockState {
  signedIn: boolean
  /** The administrator password; sign-in and the password change check it. */
  password: string
  /** Failed sign-ins (and wrong current passwords) and the lock they set (К4). */
  signIns: Tracker
  /** Wrong setup codes and the lock they set. */
  codes: Tracker
  onboarding: {
    /** The step ca is done. */
    caDone: boolean
    /** The step admin is done: there is an administrator. */
    adminDone: boolean
    /** The client entered the setup code (the sard_setup cookie, as the mocks see it). */
    setupSession: boolean
  }
  agents: Schemas['AgentDetails'][]
  sources: Schemas['Source'][]
  /** Deleted sources: gone from the list and the card, their runs and snapshots stay (soft delete, К13). */
  deletedSources: Schemas['Source'][]
  runs: Schemas['Run'][]
  /** One per source at most (F3a); a deleted source's schedule stays but is never shown. */
  schedules: Schemas['Schedule'][]
  /** The journal of each source's schedule, newest recorded first. */
  scheduleFires: Record<string, Schemas['ScheduleFire'][]>
  snapshots: Schemas['Snapshot'][]
  stepLogs: Record<string, Schemas['LogLine'][]>
  tokens: Schemas['EnrollmentToken'][]
}

function fresh(signedIn: boolean, freshInstall: boolean): MockState {
  return structuredClone({
    signedIn,
    password: MOCK_PASSWORD,
    signIns: freshTracker(),
    codes: freshTracker(),
    onboarding: { caDone: !freshInstall, adminDone: !freshInstall, setupSession: false },
    agents,
    sources,
    deletedSources,
    runs,
    schedules,
    scheduleFires,
    snapshots,
    stepLogs,
    tokens: enrollmentTokens,
  })
}

export let state: MockState = fresh(false, false)

/**
 * Back to the fixtures. Both the dev worker and Vitest start signed out (К4); by default
 * the administrator exists, [freshInstall] starts a clean installation (setup code, no admin).
 */
export function resetMockState(options: { signedIn: boolean; freshInstall?: boolean }): void {
  state = fresh(options.signedIn, options.freshInstall ?? false)
}
