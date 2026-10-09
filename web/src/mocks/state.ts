// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../api/schema'
import {
  agents,
  deletedSources,
  enrollmentTokens,
  runs,
  scheduleFires,
  schedules,
  snapshots,
  sources,
  stepLogs,
} from './fixtures'

type Schemas = components['schemas']

/** What the mock server remembers between requests; reset per test. */
export interface MockState {
  signedIn: boolean
  /** Timestamps (ms) of failed sign-ins, pruned to the 15-minute window (К4). */
  failedSignIns: number[]
  /** When the fifth failure locked sign-in (ms), or null when it is not locked (К4: matches the server). */
  lockedAt: number | null
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
  /** The self-backup's binding (F6); its agent and repository are those of the system sources. */
  selfBackup: { boundAt: string } | null
}

function fresh(signedIn: boolean): MockState {
  return structuredClone({
    signedIn,
    failedSignIns: [],
    lockedAt: null,
    agents,
    sources,
    deletedSources,
    runs,
    schedules,
    scheduleFires,
    snapshots,
    stepLogs,
    tokens: enrollmentTokens,
    selfBackup: null,
  })
}

export let state: MockState = fresh(false)

/** Back to the fixtures. Both the dev worker and Vitest start signed out (К4). */
export function resetMockState(options: { signedIn: boolean }): void {
  state = fresh(options.signedIn)
}
