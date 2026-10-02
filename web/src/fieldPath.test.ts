// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { mapField, type FieldTarget } from './fieldPath'

const configFields = ['paths', 'one_file_system', 'a/b', 'a~b']

describe('a path of the server maps to a field of the form', () => {
  test.each<[string, FieldTarget]>([
    ['name', { kind: 'name' }],
    ['agentId', { kind: 'agent' }],
    ['plugin', { kind: 'plugin' }],
    ['repositoryName', { kind: 'repository' }],
    ['config', { kind: 'config' }],
    ['config/paths', { kind: 'configField', path: ['paths'] }],
    ['config/paths/0', { kind: 'configField', path: ['paths', '0'] }],
    ['config/one_file_system', { kind: 'configField', path: ['one_file_system'] }],
    ['config/a~1b', { kind: 'configField', path: ['a/b'] }],
    ['config/a~0b', { kind: 'configField', path: ['a~b'] }],
    ['config/compression', { kind: 'config' }],
    ['ttlSeconds', { kind: 'ttl' }],
    ['label', { kind: 'label' }],
    ['', { kind: 'form' }],
    ['unknownField', { kind: 'form' }],
    ['constructor', { kind: 'form' }],
  ])('%j', (field, target) => {
    expect(mapField(field, configFields)).toEqual(target)
  })

  test('~01 is the name "~1", not "/"', () => {
    expect(mapField('config/~01', ['~1'])).toEqual({ kind: 'configField', path: ['~1'] })
  })
})
