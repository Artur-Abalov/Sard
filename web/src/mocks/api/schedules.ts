// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../../api/schema'
import { http } from '../http'
import { pageOf } from '../paging'
import { noSession, notFound, PROBLEM } from '../problems'
import { state } from '../state'
import { SERVER_TIMEZONE, schedulePreviews } from '../fixtures'
import { normalCron, validatePreview, validateSchedule } from '../validation'

type Schemas = components['schemas']

function liveSource(sourceId: string): Schemas['Source'] | undefined {
  return state.sources.find((s) => s.id === sourceId)
}

const DAY_MS = 86_400_000
const SHOWN_FIRES = 3

/** The three fires after now at a step of a day: all the mock can say of a cron it has no fixture for. */
function daily(): string[] {
  return Array.from({ length: SHOWN_FIRES }, (_, i) =>
    new Date(Date.now() + (i + 1) * DAY_MS).toISOString(),
  )
}

/** The preview of the server for [cron]: from the fixtures where it has one, a custom schedule otherwise. */
function preview(cron: string, timezone: string, lang: 'ru' | 'en'): Schemas['SchedulePreview'] {
  const known = schedulePreviews[cron]
  if (known === undefined) {
    const description = lang === 'ru' ? `Особое расписание: ${cron}` : `Custom: ${cron}`
    return { cron, timezone, description, nextFires: daily(), tooFrequent: false }
  }
  return {
    cron,
    timezone,
    description: known[lang],
    nextFires: known.nextFires,
    tooFrequent: known.tooFrequent,
  }
}

/** Stores [input] as the schedule of [sourceId], replacing the one it has. */
function save(sourceId: string, input: Schemas['ScheduleInput']): Schemas['Schedule'] {
  const now = new Date().toISOString()
  const cron = normalCron(input.cron)
  const notifyOnSuccess = input.notifyOnSuccess ?? false
  // The mock does not evaluate cron: an enabled schedule fires "in an hour", a disabled one never.
  const nextRunAt = input.enabled ? new Date(Date.now() + 3_600_000).toISOString() : null
  const existing = state.schedules.find((s) => s.sourceId === sourceId)
  if (existing !== undefined) {
    Object.assign(existing, {
      ...input,
      cron,
      notifyOnSuccess,
      nextRunAt,
      catchUpAt: null,
      updatedAt: now,
    })
    return existing
  }
  const schedule: Schemas['Schedule'] = {
    id: crypto.randomUUID(),
    sourceId,
    cron,
    timezone: input.timezone,
    enabled: input.enabled,
    nextRunAt,
    catchUpAt: null,
    lastFiredAt: null,
    skippedInRow: 0,
    notifyOnSuccess,
    lastRun: null,
    createdAt: now,
    updatedAt: now,
  }
  state.schedules.push(schedule)
  return schedule
}

export const scheduleHandlers = [
  http.get('/api/v1/schedule-preview', ({ query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const cron = query.get('cron') ?? ''
    const timezone = query.get('timezone') ?? SERVER_TIMEZONE
    const lang = query.get('lang')
    const invalid = validatePreview(cron, timezone, lang)
    if (invalid !== null) return response(422).json(invalid, PROBLEM)
    return response(200).json(preview(normalCron(cron), timezone, lang === 'ru' ? 'ru' : 'en'))
  }),

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
    return response(200).json(save(params.sourceId, input))
  }),

  http.get('/api/v1/sources/{sourceId}/schedule/fires', ({ params, query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    if (liveSource(params.sourceId) === undefined) return response(404).json(notFound, PROBLEM)
    const fires = state.scheduleFires[params.sourceId] ?? []
    return response(200).json(pageOf(fires, query.get('cursor'), Number(query.get('limit') ?? 50)))
  }),
]
