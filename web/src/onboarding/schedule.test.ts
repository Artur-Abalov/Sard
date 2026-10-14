// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { cronToTime, timeToCron } from './schedule'

// docs/specs/web/onboarding-self-backup.feature, "Время расписания переводится в cron и обратно" (Рк12).
describe('the time of the self-backup schedule', () => {
  test.each([
    ['03:00', '0 3 * * *'],
    ['23:45', '45 23 * * *'],
    ['00:00', '0 0 * * *'],
    ['09:05', '5 9 * * *'],
  ])('the time %s is the cron %s', (time, cron) => {
    expect(timeToCron(time)).toBe(cron)
  })

  test.each([
    ['0 3 * * *', '03:00'],
    ['45 23 * * *', '23:45'],
    ['5 9 * * *', '09:05'],
    ['0 0 * * *', '00:00'],
  ])('the cron %s is the time %s', (cron, time) => {
    expect(cronToTime(cron)).toBe(time)
  })

  test.each([
    '*/15 * * * *',
    '0 3 * * 1',
    '0 3 1 * *',
    '0 */2 * * *',
    '60 3 * * *',
    '0 24 * * *',
    '',
  ])('the cron %j has no time: it is shown as it is', (cron) => {
    expect(cronToTime(cron)).toBeNull()
  })

  test.each(['', '3:00', '24:00', '03:60', 'ab:cd', '03:00:00'])(
    'the text %j is no time: there is no cron for it',
    (time) => {
      expect(timeToCron(time)).toBeNull()
    },
  )
})
