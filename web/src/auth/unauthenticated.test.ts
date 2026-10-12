// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { unauthenticatedTarget } from './unauthenticated'

describe('unauthenticatedTarget (a 401 during work)', () => {
  test('a protected page goes to sign-in remembering the page', () => {
    expect(unauthenticatedTarget('/runs', '?status=failed')).toEqual({
      to: '/login',
      search: { redirect: '/runs?status=failed' },
    })
  })

  test.each(['/login', '/setup'])('%s stays where it is', (pathname) => {
    expect(unauthenticatedTarget(pathname, '')).toBeNull()
  })
})
