// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../../api/schema'
import { http } from '../http'
import { pageOf } from '../paging'
import { noSession, notFound, PROBLEM, problem, runActive } from '../problems'
import { state } from '../state'
import { validateSource } from '../validation'

type Schemas = components['schemas']

const ACTIVE: Schemas['RunStatus'][] = ['queued', 'dispatched', 'running']

/** The source's queued, dispatched or running run (D6: at most one). */
export function activeRun(sourceId: string): Schemas['Run'] | undefined {
  return state.runs.find((r) => r.sourceId === sourceId && ACTIVE.includes(r.status))
}

function queuedRun(source: Schemas['Source']): Schemas['Run'] {
  const now = new Date().toISOString()
  const step: Schemas['RunStep'] = {
    id: crypto.randomUUID(),
    ordinal: 0,
    action: 'backup',
    status: 'queued',
    phase: null,
    agentId: source.agentId,
    sourceId: source.id,
    plugin: source.plugin,
    repositoryName: source.repositoryName,
    bytesProcessed: null,
    bytesTotal: null,
    filesProcessed: null,
    filesTotal: null,
    message: null,
    backup: null,
    queuedAt: now,
    dispatchedAt: null,
    startedAt: null,
    finishedAt: null,
  }
  const run: Schemas['Run'] = {
    id: crypto.randomUUID(),
    sourceId: source.id,
    agentId: source.agentId,
    trigger: 'manual',
    status: 'queued',
    message: null,
    queuedAt: now,
    startedAt: null,
    finishedAt: null,
    steps: [step],
  }
  return run
}

export const sourceHandlers = [
  http.get('/api/v1/sources', ({ query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const agentId = query.get('agentId')
    const sources = state.sources.filter((s) => agentId === null || s.agentId === agentId)
    return response(200).json(
      pageOf(sources, query.get('cursor'), Number(query.get('limit') ?? 50)),
    )
  }),

  http.post('/api/v1/sources', async ({ request, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const input = await request.json()
    const invalid = validateSource(input, state.agents)
    if (invalid !== null) return response(422).json(invalid, PROBLEM)
    const now = new Date().toISOString()
    const source = { ...input, id: crypto.randomUUID(), createdAt: now, updatedAt: now }
    state.sources.unshift(source)
    return response(201).json(source)
  }),

  http.get('/api/v1/sources/{sourceId}', ({ params, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const source = state.sources.find((s) => s.id === params.sourceId)
    return source === undefined ? response(404).json(notFound, PROBLEM) : response(200).json(source)
  }),

  http.put('/api/v1/sources/{sourceId}', async ({ params, request, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const source = state.sources.find((s) => s.id === params.sourceId)
    if (source === undefined) return response(404).json(notFound, PROBLEM)
    const input = await request.json()
    const invalid = validateSource(input, state.agents)
    if (invalid !== null) return response(422).json(invalid, PROBLEM)
    Object.assign(source, input, { updatedAt: new Date().toISOString() })
    return response(200).json(source)
  }),

  http.delete('/api/v1/sources/{sourceId}', ({ params, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const index = state.sources.findIndex((s) => s.id === params.sourceId)
    if (index < 0) return response(404).json(notFound, PROBLEM)
    const active = activeRun(params.sourceId)
    if (active !== undefined) return response(409).json(runActive(active.id), PROBLEM)
    // Soft: the source leaves the list and the card, its runs and snapshots stay.
    state.deletedSources.push(...state.sources.splice(index, 1))
    return response(204).empty()
  }),

  http.post('/api/v1/sources/{sourceId}/runs', ({ params, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const source = state.sources.find((s) => s.id === params.sourceId)
    if (source === undefined) return response(404).json(notFound, PROBLEM)
    const active = activeRun(source.id)
    if (active !== undefined) return response(409).json(runActive(active.id), PROBLEM)
    const agent = state.agents.find((a) => a.id === source.agentId)
    if (agent?.revokedAt != null)
      return response(409).json(problem(409, 'Conflict', 'agent_revoked'), PROBLEM)
    const run = queuedRun(source)
    state.runs.unshift(run)
    return response(201).json(run)
  }),

  http.get('/api/v1/sources/{sourceId}/snapshots', ({ params, query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    // The snapshots of a deleted source stay (В7); a source that never existed is not found.
    const known = [...state.sources, ...state.deletedSources]
    if (!known.some((s) => s.id === params.sourceId)) return response(404).json(notFound, PROBLEM)
    const snapshots = state.snapshots.filter((s) => s.sourceId === params.sourceId)
    return response(200).json(
      pageOf(snapshots, query.get('cursor'), Number(query.get('limit') ?? 50)),
    )
  }),
]
