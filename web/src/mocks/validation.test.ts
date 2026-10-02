// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { agents, ids } from './fixtures'
import { validateSource, validateTtl } from './validation'

describe('validateSource', () => {
  const valid = {
    name: 'n',
    agentId: ids.dbAgent,
    plugin: 'files',
    repositoryName: 'local',
    config: {},
  }

  test('a source on a known agent, plugin and repository passes', () => {
    expect(validateSource(valid, agents)).toBeNull()
  })

  test('each unknown reference names its field and code', () => {
    const cases = [
      [{ agentId: '0192f7a0-0000-7000-8000-00000000ffff' }, 'unknown_agent', 'agentId'],
      [{ plugin: 'absent' }, 'unknown_plugin', 'plugin'],
      [{ repositoryName: 's3' }, 'unknown_repository', 'repositoryName'],
    ] as const
    for (const [change, code, field] of cases) {
      const problem = validateSource({ ...valid, ...change }, agents)
      expect(problem).toMatchObject({ status: 422, code, errors: [{ field }] })
    }
  })

  test('a plugin that does not offer backup is refused at the plugin, like the server does (K17)', () => {
    const problem = validateSource({ ...valid, plugin: 'hooks' }, agents)

    expect(problem).toMatchObject({
      status: 422,
      code: 'unknown_plugin',
      errors: [{ field: 'plugin' }],
    })
  })

  test('the plugin is checked before the repository', () => {
    const problem = validateSource({ ...valid, plugin: 'hooks', repositoryName: 's3' }, agents)

    expect(problem?.code).toBe('unknown_plugin')
  })

  test('the repository must be one the agent reported, not another agent', () => {
    const problem = validateSource(
      { ...valid, agentId: ids.webAgent, repositoryName: 'offsite' },
      agents,
    )
    expect(problem?.code).toBe('unknown_repository')
  })
})

describe('validateTtl', () => {
  test('omitted and the bounds are accepted', () => {
    for (const ttl of [undefined, null, 300, 86_400, 604_800]) expect(validateTtl(ttl)).toBeNull()
  })

  test('outside 5 minutes to 7 days names the field', () => {
    for (const ttl of [299, 604_801]) {
      expect(validateTtl(ttl)).toMatchObject({
        status: 422,
        code: 'validation_failed',
        errors: [{ field: 'ttlSeconds' }],
      })
    }
  })
})
