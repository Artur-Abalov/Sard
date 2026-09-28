// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../api/schema'
import { agents, enrollmentTokens, runs, snapshots, sources, stepLogs } from './fixtures'

type Schemas = components['schemas']

/** What the mock server remembers between requests; reset per test. */
export interface MockState {
  signedIn: boolean
  failedSignIns: number
  agents: Schemas['AgentDetails'][]
  sources: Schemas['Source'][]
  runs: Schemas['Run'][]
  snapshots: Schemas['Snapshot'][]
  stepLogs: Record<string, Schemas['LogLine'][]>
  tokens: Schemas['EnrollmentToken'][]
}

function fresh(signedIn: boolean): MockState {
  return structuredClone({
    signedIn,
    failedSignIns: 0,
    agents,
    sources,
    runs,
    snapshots,
    stepLogs,
    tokens: enrollmentTokens,
  })
}

export let state: MockState = fresh(false)

/** Back to the fixtures. The dev worker starts signed in (no sign-in page before W1b), tests signed out. */
export function resetMockState(options: { signedIn: boolean }): void {
  state = fresh(options.signedIn)
}
