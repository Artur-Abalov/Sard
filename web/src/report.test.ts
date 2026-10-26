// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { reportPeriodText, reportTitle } from './report'

describe('reportTitle', () => {
  test('names the product and the source, in the language of the console', () => {
    expect(reportTitle('db-main', 'ru')).toBe('sard — отчёт о проверке восстановления: db-main')
    expect(reportTitle('db-main', 'en')).toBe('sard — restore verification report: db-main')
  })
})

describe('reportPeriodText', () => {
  test('is the days of the zone, the last one included', () => {
    expect(
      reportPeriodText('2026-09-30T21:00:00Z', '2026-10-21T21:00:00Z', 'Europe/Moscow', 'en'),
    ).toBe('Oct 1, 2026 — Oct 21, 2026 (Europe/Moscow)')
  })

  test('in Russian', () => {
    expect(
      reportPeriodText('2026-09-30T21:00:00Z', '2026-10-21T21:00:00Z', 'Europe/Moscow', 'ru'),
    ).toBe('1 окт. 2026 г. — 21 окт. 2026 г. (Europe/Moscow)')
  })
})
