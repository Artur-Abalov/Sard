// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { canChangePassword, canSetPassword, confirmationMismatch } from './forms'

describe('canSetPassword', () => {
  test.each([
    ['', '', false],
    ['correct-horse-battery', '', false],
    ['correct-horse-battery', 'correct-horse-batter', false],
    ['correct-horse-battery', 'correct-horse-battery', true],
    // the length is the server's to judge (422), not the console's
    ['short', 'short', true],
  ])('password %j with confirmation %j: %s', (password, confirmation, expected) => {
    expect(canSetPassword(password, confirmation)).toBe(expected)
  })
})

describe('confirmationMismatch', () => {
  test('an empty confirmation is not yet a mismatch', () => {
    expect(confirmationMismatch('abc', '')).toBe(false)
  })

  test('a different confirmation is', () => {
    expect(confirmationMismatch('abc', 'abd')).toBe(true)
  })

  test('the same confirmation is not', () => {
    expect(confirmationMismatch('abc', 'abc')).toBe(false)
  })
})

describe('canChangePassword', () => {
  test.each([
    ['', 'new-password-2026', 'new-password-2026', false],
    ['old-pass', '', '', false],
    ['old-pass', 'new-password-2026', 'new-password-2025', false],
    ['old-pass', 'new-password-2026', 'new-password-2026', true],
  ])('current %j, new %j, confirmation %j: %s', (current, next, confirmation, expected) => {
    expect(canChangePassword(current, next, confirmation)).toBe(expected)
  })
})
