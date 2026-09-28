// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import en from './en.json'
import ru from './ru.json'

// Nested key paths, e.g. "login.wrongPassword", so a value moved to the wrong
// section is caught too, not just a missing leaf.
function keyPaths(value: unknown, prefix = ''): string[] {
  if (typeof value !== 'object' || value === null) return [prefix]
  return Object.entries(value).flatMap(([key, child]) =>
    keyPaths(child, prefix === '' ? key : `${prefix}.${key}`),
  )
}

describe('locales', () => {
  test('ru and en declare the same set of keys', () => {
    expect(new Set(keyPaths(ru))).toEqual(new Set(keyPaths(en)))
  })
})
