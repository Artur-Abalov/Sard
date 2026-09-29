// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { resolveRedirectTarget } from './redirect'

describe('resolveRedirectTarget', () => {
  test('an internal path with query is accepted', () => {
    expect(resolveRedirectTarget('/runs/0192f7a0-0000-7000-8000-000000000301?tab=logs')).toBe(
      '/runs/0192f7a0-0000-7000-8000-000000000301?tab=logs',
    )
  })

  test('a missing redirect goes to the home page', () => {
    expect(resolveRedirectTarget(undefined)).toBe('/')
  })

  test.each([
    '',
    'https://evil.example/',
    '//evil.example/agents',
    '/\\evil.example',
    '\\\\evil.example',
    'javascript:alert(1)',
    'agents',
    '/login',
    '/login?redirect=%2Fagents',
    '%2F%2Fevil.example',
    ' /agents',
    '/agents\u0000',
  ])('a foreign or suspicious redirect %j goes to the home page', (value) => {
    expect(resolveRedirectTarget(value)).toBe('/')
  })
})
