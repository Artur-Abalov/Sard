// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import type { components } from './api/schema'
import {
  parseRestoreSearch,
  restoreOutcome,
  restoreSearchOf,
  restorePreviewStale,
} from './restoreView'

type Output = components['schemas']['RestoreOutput']
type StepStatus = components['schemas']['StepStatus']

const output = (extra: Partial<Output>): Output => ({
  target: null,
  filesRestored: null,
  bytesRestored: null,
  database: null,
  tablesRestored: null,
  rolesRestored: null,
  ownersRestored: null,
  leftover: null,
  ...extra,
})

describe('what the page of a restore step says about its end', () => {
  test('a succeeded step shows what it restored', () => {
    const restore = output({ target: '/r/etc', filesRestored: 2, bytesRestored: 1536 })
    expect(restoreOutcome({ status: 'succeeded', restore })).toEqual({ kind: 'restored', restore })
  })

  test.each<StepStatus>(['failed', 'rejected', 'timed_out', 'cancelled'])(
    'a %s step without leftover restored nothing',
    (status) => {
      expect(restoreOutcome({ status, restore: null })).toEqual({ kind: 'nothing' })
    },
  )

  test('a leftover is a failed rollback, whatever the status', () => {
    const restore = output({ leftover: 'database app_restored_20261011' })
    expect(restoreOutcome({ status: 'failed', restore })).toEqual({
      kind: 'rollbackFailed',
      leftover: 'database app_restored_20261011',
    })
  })

  test('a lost step with a late output shows where the data is', () => {
    const restore = output({ target: '/r/etc/1', filesRestored: 2 })
    expect(restoreOutcome({ status: 'lost', restore })).toEqual({
      kind: 'late',
      target: '/r/etc/1',
    })
  })

  test('a lost step without an output says nothing more than lost', () => {
    expect(restoreOutcome({ status: 'lost', restore: null })).toBeNull()
  })

  test.each<StepStatus>(['queued', 'dispatched', 'running'])(
    'a %s step has no end yet',
    (status) => {
      expect(restoreOutcome({ status, restore: null })).toBeNull()
    },
  )

  test('a succeeded step without an output has nothing to show', () => {
    expect(restoreOutcome({ status: 'succeeded', restore: null })).toBeNull()
  })

  test('an empty leftover is no leftover', () => {
    expect(restoreOutcome({ status: 'failed', restore: output({ leftover: '' }) })).toEqual({
      kind: 'nothing',
    })
  })
})

const agent = '0192f7a0-0000-7000-8000-000000000101'

describe('the address of the restore page', () => {
  test('agent and config are read from the query', () => {
    expect(parseRestoreSearch({ agentId: agent, config: '{"destination":"a"}' })).toEqual({
      agentId: agent,
      config: { destination: 'a' },
    })
  })

  test.each([
    ['nothing', {}],
    ['a config that is not JSON', { config: '{' }],
    ['a config that is not an object', { config: '[1]' }],
    ['a config that is not text', { config: 5 }],
  ])('%s gives no config', (_name, search) => {
    expect(parseRestoreSearch(search).config).toBeNull()
  })

  test.each([
    ['not a UUID', { agentId: 'x' }],
    ['not text', { agentId: 3 }],
    ['absent', {}],
  ])('an agent id that is %s gives no agent', (_name, search) => {
    expect(parseRestoreSearch(search).agentId).toBeNull()
  })

  test('the retry of a run carries its agent and config', () => {
    expect(restoreSearchOf({ agentId: agent, restore: { config: { destination: 'a' } } })).toEqual({
      agentId: agent,
      config: '{"destination":"a"}',
    })
  })
})

describe('the preview that no longer matches the form', () => {
  const sent = { agentId: agent, config: { destination: 'a' } }

  test('the same agent and config keep the preview', () => {
    expect(restorePreviewStale(sent, { agentId: agent, config: { destination: 'a' } })).toBe(false)
  })

  test('another config drops it', () => {
    expect(restorePreviewStale(sent, { agentId: agent, config: { destination: 'b' } })).toBe(true)
  })

  test('another agent drops it', () => {
    expect(restorePreviewStale(sent, { agentId: 'other', config: { destination: 'a' } })).toBe(true)
  })

  test('no preview at all is a stale one', () => {
    expect(restorePreviewStale(null, sent)).toBe(true)
  })
})
