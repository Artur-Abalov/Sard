// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import {
  EMPTY,
  formatBytes,
  formatCatchUp,
  formatDuration,
  formatFiles,
  formatFires,
  formatMissed,
  formatPercent,
  formatRelative,
  formatScheduleTime,
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

describe('formatRelative', () => {
  const now = Date.parse('2026-09-27T10:00:00Z')
  const ago = (seconds: number) => new Date(now - seconds * 1000).toISOString()

  test.each([
    [5, '5 s ago'],
    [59, '59 s ago'],
    [60, '1 min ago'],
    [3599, '59 min ago'],
    [3600, '1 h ago'],
    [3 * 3600 + 1200, '3 h ago'],
    [86_399, '23 h ago'],
    [86_400, '1 d ago'],
    [10 * 86_400, '10 d ago'],
  ])('%s seconds back in English is %s', (seconds, text) => {
    expect(formatRelative(ago(seconds), now, 'en')).toBe(text)
  })

  test.each([
    [59, '59 с назад'],
    [120, '2 мин назад'],
    [3 * 3600, '3 ч назад'],
    [2 * 86_400, '2 д назад'],
  ])('%s seconds back in Russian is %s', (seconds, text) => {
    expect(formatRelative(ago(seconds), now, 'ru')).toBe(text)
  })

  test('a moment this close, either way, is just now', () => {
    expect(formatRelative(ago(4), now, 'en')).toBe('just now')
    expect(formatRelative(ago(-4), now, 'en')).toBe('just now')
    expect(formatRelative(ago(0), now, 'ru')).toBe('только что')
  })

  test('a moment ahead is counted forward, for a token that expires', () => {
    expect(formatRelative(ago(-2 * 3600), now, 'en')).toBe('in 2 h')
    expect(formatRelative(ago(-90), now, 'ru')).toBe('через 1 мин')
  })

  test('no time, or one that is not a time, is the placeholder', () => {
    expect(formatRelative(null, now, 'en')).toBe(EMPTY)
    expect(formatRelative('not a time', now, 'en')).toBe(EMPTY)
  })

  test('an unknown language reads as English', () => {
    expect(formatRelative(ago(3600), now, 'xx')).toBe('1 h ago')
  })
})

describe('formatScheduleTime', () => {
  test.each([
    ['2026-10-10T00:00:00Z', 'Europe/Berlin', 'ru', '02:00', '10'],
    ['2026-10-10T00:00:00Z', 'America/New_York', 'en', '20:00', '9'],
    ['2027-03-28T01:00:00Z', 'Europe/Berlin', 'ru', '03:00', '28'],
    ['2026-10-09T23:00:00Z', 'Europe/Moscow', 'en', '02:00', '10'],
  ])(
    '%s in %s on %s shows %s on day %s, not the browser’s time',
    (value, zone, language, time, day) => {
      const text = formatScheduleTime(value, zone, language)
      expect(text).toContain(time)
      expect(text).toContain(zone)
      expect(text).toMatch(new RegExp(`(^|\\D)${day}(\\D|$)`))
    },
  )

  test('no time shows a dash', () => {
    expect(formatScheduleTime(null, 'UTC', 'en')).toBe(EMPTY)
  })
})

describe('formatFires', () => {
  test.each([
    [1, 'ru', '1 срабатывание'],
    [3, 'ru', '3 срабатывания'],
    [5, 'ru', '5 срабатываний'],
    [21, 'ru', '21 срабатывание'],
    [1, 'en', '1 fire'],
    [4, 'en', '4 fires'],
  ])('%i in %s is %s', (count, language, text) => {
    expect(formatFires(count, language)).toBe(text)
  })
})

describe('formatMissed', () => {
  const missed = {
    from: '2026-10-09T13:00:00Z',
    until: '2026-10-09T16:00:00Z',
    count: 4,
    capped: false,
    timezone: 'Europe/Berlin',
  }

  test('a downtime names the count and the period in the zone of the schedule', () => {
    expect(formatMissed(missed, 'ru')).toBe(
      'сервер не работал, пропущено 4 срабатывания с 15:00 по 18:00 (9 окт. 2026 г., Europe/Berlin)',
    )
    expect(formatMissed(missed, 'en')).toBe(
      'the server was down, 4 fires missed from 15:00 to 18:00 (Oct 9, 2026, Europe/Berlin)',
    )
  })

  test('a period over midnight names both ends in full', () => {
    const text = formatMissed({ ...missed, until: '2026-10-10T08:00:00Z' }, 'en')
    expect(text).toContain('Oct 9, 2026, 15:00')
    expect(text).toContain('Oct 10, 2026, 10:00')
  })

  test('a capped count is at least the limit and names no end', () => {
    const text = formatMissed({ ...missed, count: 10_000, capped: true }, 'ru')
    expect(text).toContain('не меньше 10 000')
    expect(text).not.toContain('по ')
    expect(text).not.toContain('18:00')
  })

  test('a catch-up run is marked with the same period', () => {
    expect(formatCatchUp(missed, 'ru')).toBe(`догоняющий — ${formatMissed(missed, 'ru')}`)
  })
})
