// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { parseSearch, stringifySearch } from './searchParams'

describe('the address query of the console', () => {
  test('a parameter given once is text, given several times a list', () => {
    expect(parseSearch('?status=failed&status=running&sourceId=abc')).toEqual({
      status: ['failed', 'running'],
      sourceId: 'abc',
    })
    expect(parseSearch('?status=failed')).toEqual({ status: 'failed' })
  })

  test('nothing in it is read as JSON: a number stays text', () => {
    expect(parseSearch('?redirect=42&create=true')).toEqual({ redirect: '42', create: 'true' })
  })

  test('an empty query is no parameters', () => {
    expect(parseSearch('')).toEqual({})
    expect(parseSearch('?')).toEqual({})
  })

  test('a list is written as repeated parameters, in the order given', () => {
    expect(stringifySearch({ status: ['failed', 'running'], sourceId: 'abc' })).toBe(
      '?status=failed&status=running&sourceId=abc',
    )
  })

  test('numbers and booleans are written as text, what is undefined is left out', () => {
    expect(stringifySearch({ create: true, n: 1, gone: undefined })).toBe('?create=true&n=1')
    expect(stringifySearch({})).toBe('')
  })

  test('characters of a path in a value survive the round trip', () => {
    const search = { redirect: '/runs?status=failed&sourceId=a b' }

    expect(parseSearch(stringifySearch(search))).toEqual(search)
  })
})
