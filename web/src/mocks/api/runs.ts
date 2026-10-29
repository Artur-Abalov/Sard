// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../../api/schema'
import { http } from '../http'
import { logPage, pageOf } from '../paging'
import { noSession, notFound, PROBLEM, problem } from '../problems'
import { state } from '../state'
import { settleRuns } from './selfBackup'

type Schemas = components['schemas']

/** The run as the server shows it: the source's current name, or its name when it was deleted. */
function named(run: Schemas['Run']): Schemas['Run'] {
  const live = state.sources.find((s) => s.id === run.sourceId)
  const source = live ?? state.deletedSources.find((s) => s.id === run.sourceId)
  return { ...run, sourceName: source?.name ?? run.sourceName, sourceDeleted: live === undefined }
}

function summary(run: Schemas['Run']): Schemas['RunSummary'] {
  const { steps, ...rest } = named(run)
  // The timeout of the step the run has, as the server's list reads it from the first step.
  return { ...rest, timeoutSeconds: steps[0]?.timeoutSeconds ?? null }
}

/** Queued at or after [from] and before [to]; an absent bound is open. */
function queuedWithin(run: Schemas['Run'], from: string | null, to: string | null) {
  const queued = Date.parse(run.queuedAt)
  return (from === null || queued >= Date.parse(from)) && (to === null || queued < Date.parse(to))
}

/** A filter that is not given lets every value through. */
const same = (value: string, wanted: string | null) => wanted === null || value === wanted

function matches(
  run: Schemas['Run'],
  filter: {
    sourceId: string | null
    agentId: string | null
    status: string[]
    kind: string | null
    queuedFrom: string | null
    queuedTo: string | null
  },
) {
  return (
    same(run.sourceId, filter.sourceId) &&
    same(run.agentId, filter.agentId) &&
    same(run.kind, filter.kind) &&
    (filter.status.length === 0 || filter.status.includes(run.status)) &&
    queuedWithin(run, filter.queuedFrom, filter.queuedTo)
  )
}

const IN_PROGRESS: Schemas['RunStatus'][] = ['queued', 'dispatched', 'running']

/** What the server does with a cancel: a queued run is closed at once, one in flight is marked and waits for its agent. */
function cancelled(run: Schemas['Run']): void {
  if (run.cancellation !== null) return
  const now = new Date().toISOString()
  if (run.status !== 'queued') {
    run.outcome = 'cancelling'
    run.cancellation = { requestedAt: now, requestedBy: 'admin', result: 'pending' }
    return
  }
  run.status = 'cancelled'
  run.outcome = 'cancelled'
  run.message = 'cancelled before dispatch'
  run.finishedAt = now
  run.cancellation = { requestedAt: now, requestedBy: 'admin', result: 'cancelled' }
  run.steps = run.steps.map((step) => ({
    ...step,
    status: 'cancelled',
    message: run.message,
    finishedAt: now,
  }))
}

export const runHandlers = [
  http.get('/api/v1/runs', ({ query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    settleRuns()
    const filter = {
      sourceId: query.get('sourceId'),
      agentId: query.get('agentId'),
      status: query.getAll('status'),
      kind: query.get('kind'),
      queuedFrom: query.get('queuedFrom'),
      queuedTo: query.get('queuedTo'),
    }
    const runs = state.runs.filter((r) => matches(r, filter)).map(summary)
    return response(200).json(pageOf(runs, query.get('cursor'), Number(query.get('limit') ?? 50)))
  }),

  http.get('/api/v1/runs/{runId}', ({ params, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    settleRuns()
    const run = state.runs.find((r) => r.id === params.runId)
    return run === undefined
      ? response(404).json(notFound, PROBLEM)
      : response(200).json(named(run))
  }),

  http.post('/api/v1/runs/{runId}/cancel', ({ params, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const run = state.runs.find((r) => r.id === params.runId)
    if (run === undefined) return response(404).json(notFound, PROBLEM)
    if (!IN_PROGRESS.includes(run.status)) {
      return response(409).json(problem(409, 'Conflict', 'run_finished'), PROBLEM)
    }
    cancelled(run)
    return response(200).json(named(run))
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
