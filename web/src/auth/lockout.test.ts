// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { formatLockoutMinutes } from './lockout'

describe('formatLockoutMinutes', () => {
  test.each([
    [900, 15],
    [61, 2],
    [60, 1],
    [1, 1],
  ])('%i seconds is shown as %i minutes, rounded up', (seconds, minutes) => {
    expect(formatLockoutMinutes(seconds)).toBe(minutes)
  })
})
