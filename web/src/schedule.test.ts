// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import {
  cronOf,
  DEFAULT_PRESET,
  draftOf,
  inputOf,
  presetOf,
  scheduleFieldTarget,
  toCronMode,
  withKind,
  type Preset,
} from './schedule'

describe('a ready variant gives the canonical cron', () => {
  test.each([
    [{ kind: 'daily', time: '02:00' }, '0 2 * * *'],
    [{ kind: 'daily', time: '23:05' }, '5 23 * * *'],
    [{ kind: 'hourly', minute: 0 }, '0 * * * *'],
    [{ kind: 'hourly', minute: 15 }, '15 * * * *'],
    [{ kind: 'weekdays', time: '02:30' }, '30 2 * * 1-5'],
    [{ kind: 'weekly', day: 0, time: '03:00' }, '0 3 * * 0'],
    [{ kind: 'weekly', day: 6, time: '00:00' }, '0 0 * * 6'],
    [{ kind: 'cron', text: '*/5 * * * *' }, '*/5 * * * *'],
  ] as [Preset, string][])('%j is %s', (preset, cron) => {
    expect(cronOf(preset)).toBe(cron)
  })

  test('a time that is not complete gives no cron, and the server answers for the field', () => {
    expect(cronOf({ kind: 'daily', time: '' })).toBe('')
    expect(cronOf({ kind: 'hourly', minute: Number.NaN })).toBe('')
  })
})

describe('a saved cron opens as its variant or in cron mode', () => {
  test.each([
    ['0 2 * * *', { kind: 'daily', time: '02:00' }],
    ['15 * * * *', { kind: 'hourly', minute: 15 }],
    ['30 2 * * 1-5', { kind: 'weekdays', time: '02:30' }],
    ['0 3 * * 0', { kind: 'weekly', day: 0, time: '03:00' }],
    ['0 3 * * 7', { kind: 'cron', text: '0 3 * * 7' }],
    ['0 2 * * MON-FRI', { kind: 'cron', text: '0 2 * * MON-FRI' }],
    ['*/15 * * * *', { kind: 'cron', text: '*/15 * * * *' }],
    ['0 2 1-7 * 1', { kind: 'cron', text: '0 2 1-7 * 1' }],
    ['00 2 * * *', { kind: 'cron', text: '00 2 * * *' }],
    ['60 * * * *', { kind: 'cron', text: '60 * * * *' }],
    ['0 24 * * *', { kind: 'cron', text: '0 24 * * *' }],
  ] as [string, Preset][])('%s opens as %j', (cron, preset) => {
    expect(presetOf(cron)).toEqual(preset)
  })

  test('every ready variant survives the round trip', () => {
    const variants: Preset[] = [
      { kind: 'daily', time: '09:07' },
      { kind: 'hourly', minute: 59 },
      { kind: 'weekdays', time: '23:59' },
      { kind: 'weekly', day: 3, time: '04:00' },
    ]
    for (const variant of variants) expect(presetOf(cronOf(variant))).toEqual(variant)
  })
})

describe('switching the editor', () => {
  test('to the cron mode carries the cron of the variant into the field', () => {
    expect(toCronMode({ kind: 'weekdays', time: '02:30' })).toEqual({
      kind: 'cron',
      text: '30 2 * * 1-5',
    })
  })

  test('between ready variants keeps the time that carries over', () => {
    expect(withKind({ kind: 'daily', time: '04:30' }, 'weekdays')).toEqual({
      kind: 'weekdays',
      time: '04:30',
    })
    expect(withKind({ kind: 'daily', time: '04:30' }, 'weekly')).toEqual({
      kind: 'weekly',
      day: 0,
      time: '04:30',
    })
    expect(withKind({ kind: 'hourly', minute: 15 }, 'daily')).toEqual({
      kind: 'daily',
      time: '02:00',
    })
    expect(withKind({ kind: 'daily', time: '04:30' }, 'hourly')).toEqual({
      kind: 'hourly',
      minute: 0,
    })
    expect(withKind({ kind: 'daily', time: '04:30' }, 'daily')).toEqual({
      kind: 'daily',
      time: '04:30',
    })
    expect(withKind({ kind: 'daily', time: '04:30' }, 'cron')).toEqual({
      kind: 'cron',
      text: '30 4 * * *',
    })
  })
})

describe('the field of an error', () => {
  test.each([
    ['cron', 'cron'],
    ['timezone', 'timezone'],
    ['', 'form'],
    ['unknown', 'form'],
  ] as const)('%j belongs to %s', (field, target) => {
    expect(scheduleFieldTarget(field)).toBe(target)
  })
})

describe('the editor and the request', () => {
  const saved = {
    id: 'i',
    sourceId: 's',
    cron: '30 2 * * 1-5',
    timezone: 'Europe/Berlin',
    enabled: false,
    nextRunAt: null,
    catchUpAt: null,
    lastFiredAt: null,
    skippedInRow: 0,
    notifyOnSuccess: true,
    lastRun: null,
    createdAt: '2026-10-09T12:00:00Z',
    updatedAt: '2026-10-09T12:00:00Z',
  }

  test('a new schedule is every day at 02:00, enabled, in the zone of the server', () => {
    expect(draftOf(null)).toEqual({
      preset: DEFAULT_PRESET,
      timezone: null,
      enabled: true,
      notifyOnSuccess: false,
    })
  })

  test('a saved schedule opens with its values', () => {
    expect(draftOf(saved)).toEqual({
      preset: { kind: 'weekdays', time: '02:30' },
      timezone: 'Europe/Berlin',
      enabled: false,
      notifyOnSuccess: true,
    })
  })

  test('the request carries the cron, the zone, the switches', () => {
    expect(inputOf(draftOf(saved), undefined)).toEqual({
      cron: '30 2 * * 1-5',
      timezone: 'Europe/Berlin',
      enabled: false,
      notifyOnSuccess: true,
    })
  })

  test('a zone not picked is the one the preview named', () => {
    expect(inputOf(draftOf(null), 'Europe/Moscow')?.timezone).toBe('Europe/Moscow')
    expect(inputOf({ ...draftOf(null), timezone: 'Asia/Tokyo' }, 'Europe/Moscow')?.timezone).toBe(
      'Asia/Tokyo',
    )
  })

  test('without a picked zone and without a preview there is nothing to send', () => {
    expect(inputOf(draftOf(null), undefined)).toBeNull()
  })
})
