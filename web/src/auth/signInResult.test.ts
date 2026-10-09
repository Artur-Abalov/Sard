// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { signInResult } from './signInResult'

describe('signInResult', () => {
  test.each([
    [204, null, { kind: 'ok' }],
    [409, null, { kind: 'setup' }],
    [401, null, { kind: 'invalid' }],
    [429, '900', { kind: 'locked', minutes: 15 }],
    [429, null, { kind: 'locked', minutes: 0 }],
    [503, null, { kind: 'unavailable' }],
    [500, null, { kind: 'unavailable' }],
  ])('status %i with Retry-After %j', (status, retryAfter, expected) => {
    expect(signInResult(status, retryAfter)).toEqual(expected)
  })
})
