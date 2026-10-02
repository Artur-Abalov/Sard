// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { http } from '../http'
import { noSession, PROBLEM } from '../problems'
import { state } from '../state'

// The counts and the first steps by the rules of docs/specs/server/rest-api-w2.feature.
export const overviewHandlers = [
  http.get('/api/v1/overview', ({ response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const live = state.agents.filter((a) => a.revokedAt === null)
    const steps = {
      tokenIssued: state.tokens.length > 0,
      agentConnected: live.some((a) => a.lastSeenAt !== null),
      repositoryInitialized: live.some((a) => a.repositories.some((r) => r.repositoryId !== null)),
      sourceCreated: state.sources.length > 0,
      backupSucceeded: state.runs.some((r) => r.status === 'succeeded'),
    }
    return response(200).json({
      agentsOnline: live.filter((a) => a.status === 'online').length,
      agentsTotal: live.length,
      firstSteps: { ...steps, complete: Object.values(steps).every(Boolean) },
    })
  }),
]
