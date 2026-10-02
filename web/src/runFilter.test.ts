// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { parseRunFilter } from './runFilter'

const uuid = '0192f7a0-0000-7000-8000-000000000201'

describe('the run filter in the address', () => {
  test.each([
    ['status=failed', { status: 'failed' }, { statuses: ['failed'], sourceId: null }],
    [
      'status=failed&status=running',
      { status: ['failed', 'running'] },
      { statuses: ['failed', 'running'], sourceId: null },
    ],
    ['status=done', { status: 'done' }, { statuses: [], sourceId: null }],
    [
      'a mix of known and unknown statuses',
      { status: ['failed', 'done'] },
      { statuses: ['failed'], sourceId: null },
    ],
    ['sourceId=not-a-uuid', { sourceId: 'not-a-uuid' }, { statuses: [], sourceId: null }],
    ['a source id', { sourceId: uuid }, { statuses: [], sourceId: uuid }],
    ['a source id that is not text', { sourceId: 42 }, { statuses: [], sourceId: null }],
    ['nothing', {}, { statuses: [], sourceId: null }],
    [
      'a repeated status once',
      { status: ['failed', 'failed'] },
      { statuses: ['failed'], sourceId: null },
    ],
  ])('%s', (_name, search, filter) => {
    expect(parseRunFilter(search)).toEqual(filter)
  })
})
