// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { groupFieldErrors, targetKey } from './fieldErrors'

const configFields = ['paths', 'one_file_system']

describe('errors of the server grouped by the field they belong to', () => {
  test('each error lands at its own field, all at once', () => {
    const grouped = groupFieldErrors(
      [
        { field: 'config/one_file_system', text: { server: 'must be a boolean' } },
        { field: 'config/paths/0', text: { server: 'must be absolute' } },
        { field: 'agentId', text: { key: 'errors.agent_revoked' } },
      ],
      configFields,
    )

    expect(grouped).toEqual({
      'config:one_file_system': [{ server: 'must be a boolean' }],
      'config:paths/0': [{ server: 'must be absolute' }],
      agent: [{ key: 'errors.agent_revoked' }],
    })
  })

  test('an error of a field the form has no place for goes to the config as a whole', () => {
    const grouped = groupFieldErrors(
      [{ field: 'config/compression', text: { server: 'unknown' } }],
      configFields,
    )

    expect(grouped).toEqual({ config: [{ server: 'unknown' }] })
  })

  test('several errors of one field are kept in order', () => {
    const grouped = groupFieldErrors(
      [
        { field: 'name', text: { key: 'errors.validation_failed' } },
        { field: 'name', text: { server: 'too long' } },
      ],
      configFields,
    )

    expect(grouped.name).toEqual([{ key: 'errors.validation_failed' }, { server: 'too long' }])
  })

  test('the target of a config field is named by its path', () => {
    expect(targetKey({ kind: 'configField', path: ['paths', '0'] })).toBe('config:paths/0')
    expect(targetKey({ kind: 'ttl' })).toBe('ttl')
  })
})
