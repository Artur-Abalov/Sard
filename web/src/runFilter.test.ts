// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { parseRunFilter } from './runFilter'

const uuid = '0192f7a0-0000-7000-8000-000000000201'

describe('the run filter in the address', () => {
  test.each([
    ['status=failed', { status: 'failed' }, { statuses: ['failed'], sourceId: null, kind: null }],
    [
      'status=failed&status=running',
      { status: ['failed', 'running'] },
      { statuses: ['failed', 'running'], sourceId: null, kind: null },
    ],
    ['status=done', { status: 'done' }, { statuses: [], sourceId: null, kind: null }],
    [
      'a mix of known and unknown statuses',
      { status: ['failed', 'done'] },
      { statuses: ['failed'], sourceId: null, kind: null },
    ],
    [
      'sourceId=not-a-uuid',
      { sourceId: 'not-a-uuid' },
      { statuses: [], sourceId: null, kind: null },
    ],
    ['a source id', { sourceId: uuid, kind: null }, { statuses: [], sourceId: uuid, kind: null }],
    [
      'a source id that is not text',
      { sourceId: 42 },
      { statuses: [], sourceId: null, kind: null },
    ],
    ['nothing', {}, { statuses: [], sourceId: null, kind: null }],
    [
      'a repeated status once',
      { status: ['failed', 'failed'] },
      { statuses: ['failed'], sourceId: null, kind: null },
    ],
  ])('%s', (_name, search, filter) => {
    expect(parseRunFilter(search)).toEqual(filter)
  })
})

describe('the kind of run in the address', () => {
  test.each([
    ['restore', { kind: 'restore' }, 'restore'],
    ['backup', { kind: 'backup' }, 'backup'],
    ['something else', { kind: 'verify' }, null],
    ['a list', { kind: ['restore', 'backup'] }, null],
    ['nothing', {}, null],
  ])('%s', (_name, search, kind) => {
    expect(parseRunFilter(search).kind).toBe(kind)
  })
})
