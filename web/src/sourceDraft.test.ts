// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import filesSchema from '../../agent/plugins/files/schema.json'
import {
  buildSourceInput,
  draftOf,
  emptyDraft,
  offeredAgents,
  offeredPlugins,
  withAgent,
  withPlugin,
} from './sourceDraft'

const files = {
  name: 'files',
  version: '1',
  actions: ['backup', 'restore'],
  configSchema: filesSchema,
}
const hooks = { name: 'hooks', version: '1', actions: ['run'], configSchema: {} }
const nested = {
  name: 'nested',
  version: '1',
  actions: ['backup'],
  configSchema: { type: 'object', properties: { a: { type: 'object' } } },
}

describe('what a source can be made of', () => {
  test('revoked agents are not offered', () => {
    const agents = [
      { id: 'x', revokedAt: null },
      { id: 'y', revokedAt: '2026-09-27T09:00:00Z' },
    ]

    expect(offeredAgents(agents).map((a) => a.id)).toEqual(['x'])
  })

  test('a plugin without the backup action is not offered', () => {
    expect(offeredPlugins({ plugins: [files, hooks] } as never).map((p) => p.name)).toEqual([
      'files',
    ])
  })
})

describe('the draft of a source', () => {
  const filled = {
    ...emptyDraft('x'),
    name: 'etc',
    plugin: 'files',
    repository: 'local',
    values: { paths: ['/etc'] },
  }

  test('choosing another agent clears the plugin, the repository and the config', () => {
    const next = withAgent(filled, 'y')

    expect(next).toEqual({ ...emptyDraft('y'), name: 'etc' })
  })

  test('choosing another plugin clears the config and starts the fields of its schema', () => {
    const next = withPlugin(filled, files as never)

    expect(next.plugin).toBe('files')
    expect(next.values).toEqual({ paths: [], exclude: [], one_file_system: false })
    expect(next.repository).toBe('local')
  })

  test('a plugin the form cannot lay out starts an empty JSON object', () => {
    expect(withPlugin(filled, nested as never).json).toBe('{}')
  })

  test('an existing source fills the draft', () => {
    const draft = draftOf(
      {
        name: 'etc',
        agentId: 'x',
        plugin: 'files',
        repositoryName: 'local',
        config: { paths: ['/etc'] },
      },
      files as never,
    )

    expect(draft).toMatchObject({
      name: 'etc',
      agentId: 'x',
      plugin: 'files',
      repository: 'local',
      values: { paths: ['/etc'], exclude: [], one_file_system: false },
    })
  })

  test('a source whose plugin the agent no longer offers has no plugin chosen', () => {
    const draft = draftOf(
      { name: 'etc', agentId: 'x', plugin: 'gone', repositoryName: 'local', config: { a: 1 } },
      undefined,
    )

    expect(draft.plugin).toBe('')
    expect(draft.json).toBe('{\n  "a": 1\n}')
  })
})

describe('the request that saves a source', () => {
  test('carries the name, the agent, the plugin, the repository and the config of the form', () => {
    const draft = {
      ...emptyDraft('x'),
      name: 'etc',
      plugin: 'files',
      repository: 'local',
      values: { paths: ['/etc'], exclude: [], one_file_system: false },
    }

    expect(buildSourceInput(draft, files as never)).toEqual({
      ok: true,
      input: {
        name: 'etc',
        agentId: 'x',
        plugin: 'files',
        repositoryName: 'local',
        config: { paths: ['/etc'], one_file_system: false },
      },
    })
  })

  test('takes the config of a JSON editor as it is typed', () => {
    const draft = { ...emptyDraft('x'), plugin: 'nested', json: '{"a":{"b":1}}' }

    expect(buildSourceInput(draft, nested as never)).toMatchObject({
      ok: true,
      input: { config: { a: { b: 1 } } },
    })
  })

  test('JSON that is not an object is not sent', () => {
    const draft = { ...emptyDraft('x'), plugin: 'nested', json: '[' }

    expect(buildSourceInput(draft, nested as never)).toEqual({ ok: false })
  })

  test('nothing about the values is checked: an empty form is sent', () => {
    const draft = { ...emptyDraft('x'), plugin: 'files' }

    expect(buildSourceInput(draft, files as never)).toMatchObject({ ok: true })
  })
})
