// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../../api/schema'
import { http } from '../http'
import { pageOf } from '../paging'
import { noSession, notFound, PROBLEM } from '../problems'
import { state } from '../state'

type Schemas = components['schemas']

function summary(agent: Schemas['AgentDetails']): Schemas['AgentSummary'] {
  const {
    id,
    hostname,
    status,
    agentVersion,
    os,
    arch,
    registeredAt,
    lastSeenAt,
    revokedAt,
    duplicateSessionAt,
  } = agent
  return {
    id,
    hostname,
    status,
    agentVersion,
    os,
    arch,
    registeredAt,
    lastSeenAt,
    revokedAt,
    duplicateSessionAt,
  }
}

const ACTIVE = ['queued', 'dispatched', 'running']

// Revoking an agent ends its waiting and running steps as lost, and their runs fail (S8b В5).
function loseActiveSteps(agentId: string) {
  const now = new Date().toISOString()
  for (const run of state.runs) {
    for (const step of run.steps) {
      if (step.agentId !== agentId || !ACTIVE.includes(step.status)) continue
      Object.assign(step, { status: 'lost', message: 'agent revoked', finishedAt: now })
      Object.assign(run, { status: 'failed', message: 'agent revoked', finishedAt: now })
    }
  }
}

export const agentHandlers = [
  http.get('/api/v1/agents', ({ query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const status = query.get('status')
    const agents = state.agents.filter((a) => status === null || a.status === status).map(summary)
    return response(200).json(pageOf(agents, query.get('cursor'), Number(query.get('limit') ?? 50)))
  }),

  http.get('/api/v1/agents/{agentId}', ({ params, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const agent = state.agents.find((a) => a.id === params.agentId)
    return agent === undefined ? response(404).json(notFound, PROBLEM) : response(200).json(agent)
  }),

  http.post('/api/v1/agents/{agentId}/revoke', ({ params, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const agent = state.agents.find((a) => a.id === params.agentId)
    if (agent === undefined) return response(404).json(notFound, PROBLEM)
    // Revoking a revoked agent changes nothing; otherwise it goes offline for good.
    if (agent.revokedAt === null) {
      Object.assign(agent, { revokedAt: new Date().toISOString(), status: 'offline' })
      loseActiveSteps(agent.id)
    }
    return response(200).json(agent)
  }),
]
