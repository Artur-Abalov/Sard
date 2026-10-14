// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { canRevokeToken, shortId, ttlSeconds } from './tokens'

describe('the lifetime of a new token', () => {
  test.each([
    ['', 'hours', undefined],
    [2, 'hours', 7200],
    ['2', 'hours', 7200],
    [30, 'minutes', 1800],
    [1, 'days', 86_400],
    [0.5, 'hours', 1800],
  ] as const)('%j %s is %j seconds', (value, unit, seconds) => {
    expect(ttlSeconds(value, unit)).toBe(seconds)
  })

  test('text that is no number is left for the server to refuse', () => {
    expect(ttlSeconds('abc', 'hours')).toBeUndefined()
  })
})

describe('the revoke button', () => {
  test.each([
    ['active', true],
    ['used', false],
    ['expired', false],
    ['revoked', false],
  ] as const)('a %s token: %s', (status, shown) => {
    expect(canRevokeToken(status)).toBe(shown)
  })
})

describe('shortId', () => {
  test('is the first eight characters of the id', () => {
    expect(shortId('0192f7a0-0000-7000-8000-000000000101')).toBe('0192f7a0')
  })
})
