// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { EMPTY, formatTimestamp } from './format'

describe('formatTimestamp', () => {
  test('no verified restore yet shows a dash', () => {
    expect(formatTimestamp(null, 'ru')).toBe('—')
    expect(EMPTY).toBe('—')
  })

  test('a timestamp is shown in the requested locale, in UTC', () => {
    const value = '2026-09-01T03:04:00Z'
    expect(formatTimestamp(value, 'en')).toBe('Sep 1, 2026, 3:04 AM')
    expect(formatTimestamp(value, 'ru')).toBe('1 сент. 2026 г., 03:04')
  })
})
