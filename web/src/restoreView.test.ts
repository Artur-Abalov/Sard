// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { parseRestoreSearch, restoreSearchOf, restorePreviewStale } from './restoreView'

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
