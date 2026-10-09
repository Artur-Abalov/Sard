// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../../api/schema'
import { http } from '../http'
import { pageOf } from '../paging'
import { noSession, notFound, PROBLEM } from '../problems'
import { state } from '../state'
import { validateSchedule } from '../validation'

type Schemas = components['schemas']

function liveSource(sourceId: string): Schemas['Source'] | undefined {
  return state.sources.find((s) => s.id === sourceId)
}

export const scheduleHandlers = [
  http.get('/api/v1/sources/{sourceId}/schedule', ({ params, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const schedule = state.schedules.find((s) => s.sourceId === params.sourceId)
    if (liveSource(params.sourceId) === undefined || schedule === undefined) {
      return response(404).json(notFound, PROBLEM)
    }
    return response(200).json(schedule)
  }),

  http.put('/api/v1/sources/{sourceId}/schedule', async ({ params, request, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    if (liveSource(params.sourceId) === undefined) return response(404).json(notFound, PROBLEM)
    const input = await request.json()
    const invalid = validateSchedule(input)
    if (invalid !== null) return response(422).json(invalid, PROBLEM)
    const now = new Date().toISOString()
    const cron = input.cron.trim().split(/\s+/).join(' ')
    // The mock does not evaluate cron: an enabled schedule fires "in an hour", a disabled one never.
    const nextRunAt = input.enabled ? new Date(Date.now() + 3_600_000).toISOString() : null
    const existing = state.schedules.find((s) => s.sourceId === params.sourceId)
    if (existing !== undefined) {
      Object.assign(existing, { ...input, cron, nextRunAt, catchUpAt: null, updatedAt: now })
      return response(200).json(existing)
    }
    const schedule: Schemas['Schedule'] = {
      id: crypto.randomUUID(),
      sourceId: params.sourceId,
      cron,
      timezone: input.timezone,
      enabled: input.enabled,
      nextRunAt,
      catchUpAt: null,
      lastFiredAt: null,
      skippedInRow: 0,
      createdAt: now,
      updatedAt: now,
    }
    state.schedules.push(schedule)
    return response(200).json(schedule)
  }),

  http.get('/api/v1/sources/{sourceId}/schedule/fires', ({ params, query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    if (liveSource(params.sourceId) === undefined) return response(404).json(notFound, PROBLEM)
    const fires = state.scheduleFires[params.sourceId] ?? []
    return response(200).json(pageOf(fires, query.get('cursor'), Number(query.get('limit') ?? 50)))
  }),
]
