// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../../api/schema'
import { http } from '../http'
import { logPage, pageOf } from '../paging'
import { noSession, notFound, PROBLEM } from '../problems'
import { state } from '../state'

type Schemas = components['schemas']

function summary(run: Schemas['Run']): Schemas['RunSummary'] {
  const { steps: _steps, ...rest } = run
  return rest
}

/** Queued at or after [from] and before [to]; an absent bound is open. */
function queuedWithin(run: Schemas['Run'], from: string | null, to: string | null) {
  const queued = Date.parse(run.queuedAt)
  return (from === null || queued >= Date.parse(from)) && (to === null || queued < Date.parse(to))
}

function matches(
  run: Schemas['Run'],
  filter: {
    sourceId: string | null
    agentId: string | null
    status: string[]
    queuedFrom: string | null
    queuedTo: string | null
  },
) {
  return (
    (filter.sourceId === null || run.sourceId === filter.sourceId) &&
    (filter.agentId === null || run.agentId === filter.agentId) &&
    (filter.status.length === 0 || filter.status.includes(run.status)) &&
    queuedWithin(run, filter.queuedFrom, filter.queuedTo)
  )
}

export const runHandlers = [
  http.get('/api/v1/runs', ({ query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const filter = {
      sourceId: query.get('sourceId'),
      agentId: query.get('agentId'),
      status: query.getAll('status'),
      queuedFrom: query.get('queuedFrom'),
      queuedTo: query.get('queuedTo'),
    }
    const runs = state.runs.filter((r) => matches(r, filter)).map(summary)
    return response(200).json(pageOf(runs, query.get('cursor'), Number(query.get('limit') ?? 50)))
  }),

  http.get('/api/v1/runs/{runId}', ({ params, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const run = state.runs.find((r) => r.id === params.runId)
    return run === undefined ? response(404).json(notFound, PROBLEM) : response(200).json(run)
  }),

  http.get('/api/v1/runs/{runId}/steps/{stepId}/logs', ({ params, query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const run = state.runs.find((r) => r.id === params.runId)
    if (run === undefined || !run.steps.some((s) => s.id === params.stepId)) {
      return response(404).json(notFound, PROBLEM)
    }
    const lines = state.stepLogs[params.stepId] ?? []
    return response(200).json(
      logPage(lines, Number(query.get('afterSeq') ?? 0), Number(query.get('limit') ?? 500)),
    )
  }),
]
