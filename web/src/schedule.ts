// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'

type Schemas = components['schemas']

// Previews are asked for this long after the last change of the schedule fields (F3b, ВП2).
export const PREVIEW_DEBOUNCE_MS = 300

// 0 is Sunday, as in cron.
export type Weekday = 0 | 1 | 2 | 3 | 4 | 5 | 6
export const WEEKDAYS: readonly Weekday[] = [1, 2, 3, 4, 5, 6, 0]

// What the editor lets one pick: a ready variant that stands for one canonical cron, or the cron itself.
// The console never judges a cron (the server does); it only translates a variant into text and back.
export type Preset =
  | { kind: 'daily'; time: string }
  | { kind: 'hourly'; minute: number }
  | { kind: 'weekdays'; time: string }
  | { kind: 'weekly'; day: Weekday; time: string }
  | { kind: 'cron'; text: string }

export type PresetKind = Preset['kind']
export const PRESET_KINDS: readonly PresetKind[] = ['daily', 'hourly', 'weekdays', 'weekly', 'cron']

// A new schedule: every day at 02:00 (ВП1).
const DEFAULT_TIME = '02:00'
export const DEFAULT_PRESET: Preset = { kind: 'daily', time: DEFAULT_TIME }

const TIME = /^(\d{2}):(\d{2})$/
const CANONICAL = /^(\d{1,2}) (\d{1,2}) \* \* (\*|1-5|[0-6])$/
const HOURLY = /^(\d{1,2}) \* \* \* \*$/
const MAX_MINUTE = 59
const MAX_HOUR = 23

function pad(value: number): string {
  return String(value).padStart(2, '0')
}

// "HH:MM" as the pair of cron numbers (minute, hour), or null when it is not a time.
function clock(time: string): [string, string] | null {
  const match = TIME.exec(time)
  return match === null ? null : [String(Number(match[2])), String(Number(match[1]))]
}

// The canonical cron of a ready variant; the text of the cron variant as it stands. A time that is
// not complete yet gives no cron at all, so the server answers for the field.
export function cronOf(preset: Preset): string {
  if (preset.kind === 'cron') return preset.text
  if (preset.kind === 'hourly')
    return Number.isInteger(preset.minute) ? `${preset.minute} * * * *` : ''
  const time = clock(preset.time)
  if (time === null) return ''
  const [minute, hour] = time
  const days = {
    daily: '*',
    weekdays: '1-5',
    weekly: String(preset.kind === 'weekly' ? preset.day : ''),
  }
  return `${minute} ${hour} * * ${days[preset.kind]}`
}

// A canonical number: no leading zero and in range, else null.
function number(token: string, max: number): number | null {
  const value = Number(token)
  return String(value) === token && value <= max ? value : null
}

function hourlyOf(cron: string): Preset | null {
  const match = HOURLY.exec(cron)
  const minute = match === null ? null : number(match[1], MAX_MINUTE)
  return minute === null ? null : { kind: 'hourly', minute }
}

function timedOf(cron: string): Preset | null {
  const match = CANONICAL.exec(cron)
  if (match === null) return null
  const minute = number(match[1], MAX_MINUTE)
  const hour = number(match[2], MAX_HOUR)
  if (minute === null || hour === null) return null
  const time = `${pad(hour)}:${pad(minute)}`
  if (match[3] === '*') return { kind: 'daily', time }
  if (match[3] === '1-5') return { kind: 'weekdays', time }
  return { kind: 'weekly', day: Number(match[3]) as Weekday, time }
}

// The ready variant a saved cron is the canonical form of; any other cron opens in cron mode as written.
export function presetOf(cron: string): Preset {
  return hourlyOf(cron) ?? timedOf(cron) ?? { kind: 'cron', text: cron }
}

// Switching a variant to the cron mode carries its cron into the field.
export function toCronMode(preset: Preset): Preset {
  return { kind: 'cron', text: cronOf(preset) }
}

// A variant of a kind with the values the previous one had, where they carry over.
export function withKind(preset: Preset, kind: PresetKind): Preset {
  if (kind === preset.kind) return preset
  if (kind === 'cron') return toCronMode(preset)
  const time = 'time' in preset ? preset.time : DEFAULT_TIME
  const minute = preset.kind === 'hourly' ? preset.minute : 0
  const shapes: Record<Exclude<PresetKind, 'cron'>, Preset> = {
    daily: { kind: 'daily', time },
    hourly: { kind: 'hourly', minute },
    weekdays: { kind: 'weekdays', time },
    weekly: { kind: 'weekly', day: 0, time },
  }
  return shapes[kind]
}

// Where in the editor an error of the server belongs.
export type ScheduleFieldTarget = 'cron' | 'timezone' | 'form'

export function scheduleFieldTarget(field: string): ScheduleFieldTarget {
  return field === 'cron' || field === 'timezone' ? field : 'form'
}

// What the editor holds. A time zone not picked yet is the server's: the preview names it.
export interface ScheduleDraft {
  preset: Preset
  timezone: string | null
  enabled: boolean
  notifyOnSuccess: boolean
}

export function draftOf(schedule: Schemas['Schedule'] | null): ScheduleDraft {
  if (schedule === null) {
    return { preset: DEFAULT_PRESET, timezone: null, enabled: true, notifyOnSuccess: false }
  }
  return {
    preset: presetOf(schedule.cron),
    timezone: schedule.timezone,
    enabled: schedule.enabled,
    notifyOnSuccess: schedule.notifyOnSuccess,
  }
}

// The body of the PUT: the zone the preview named when none was picked, UTC when even that is unknown.
export function inputOf(
  draft: ScheduleDraft,
  previewTimezone: string | undefined,
): Schemas['ScheduleInput'] {
  return {
    cron: cronOf(draft.preset),
    timezone: draft.timezone ?? previewTimezone ?? 'UTC',
    enabled: draft.enabled,
    notifyOnSuccess: draft.notifyOnSuccess,
  }
}
