// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import {
  EMPTY,
  formatBytes,
  formatDuration,
  formatFiles,
  formatPercent,
  formatTimestamp,
} from './format'

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

describe('formatBytes', () => {
  test.each([
    [0, 'en', '0 B'],
    [1023, 'en', '1023 B'],
    [1024, 'en', '1 KiB'],
    [1536, 'en', '1.5 KiB'],
    [734003200, 'en', '700 MiB'],
    [2147483648, 'en', '2 GiB'],
    [1536, 'ru', '1,5 КиБ'],
    [2147483648, 'ru', '2 ГиБ'],
    [null, 'en', '—'],
  ])('%s bytes in %s is %s', (bytes, language, text) => {
    expect(formatBytes(bytes, language)).toBe(text)
  })

  test('sizes above the largest unit stay in it', () => {
    expect(formatBytes(2 * 1024 ** 5, 'en')).toBe('2048 TiB')
  })
})

describe('formatPercent', () => {
  test.each([
    [734003200, 2147483648, 34],
    [0, 1000, 0],
    [1000, 1000, 100],
    [1500, 1000, 100],
    [100, null, null],
    [null, 1000, null],
    [100, 0, null],
  ])('%s of %s is %s', (processed, total, percent) => {
    expect(formatPercent(processed, total)).toBe(percent)
  })
})

describe('formatDuration', () => {
  const now = Date.parse('2026-09-27T10:00:00Z')

  test.each([
    ['2026-09-25T21:00:01Z', '2026-09-25T21:02:31Z', '2 min 30 s'],
    ['2026-09-25T21:00:01Z', '2026-09-25T21:00:01Z', '0 s'],
    ['2026-09-25T21:00:00Z', '2026-09-25T22:00:00Z', '1 h 0 min'],
    ['2026-09-27T09:58:02Z', null, '1 min 58 s'],
    [null, null, '—'],
  ])('from %s to %s is %s', (start, end, text) => {
    expect(formatDuration(start, end, now, 'en')).toBe(text)
  })

  test('the unit names follow the language', () => {
    expect(formatDuration('2026-09-25T21:00:01Z', '2026-09-25T21:02:31Z', now, 'ru')).toBe(
      '2 мин 30 с',
    )
  })

  test('a clock that is behind the start never gives a negative duration', () => {
    expect(formatDuration('2026-09-27T10:00:05Z', null, now, 'en')).toBe('0 s')
  })
})

describe('formatFiles', () => {
  test.each([
    [1200, 3400, '1\u00a0200 из 3\u00a0400 файлов'],
    [1200, null, '1\u00a0200 файлов'],
    [null, null, null],
    [null, 3400, null],
  ])('%s of %s files in ru is %s', (processed, total, text) => {
    expect(formatFiles(processed, total, 'ru')).toBe(text)
  })

  test('English', () => {
    expect(formatFiles(1200, 3400, 'en')).toBe('1,200 of 3,400 files')
    expect(formatFiles(1200, null, 'en')).toBe('1,200 files')
  })
})
