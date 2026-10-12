// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { bannerTone } from './selfBackupBanner'

describe('the self-backup banner tone', () => {
  test('no warnings, no banner', () => {
    expect(bannerTone([])).toBeNull()
  })

  test('a setting alone is a warning', () => {
    expect(bannerTone(['local_storage'])).toBe('warning')
    expect(bannerTone(['schedule_disabled', 'local_storage'])).toBe('warning')
  })

  test('a broken backup is an error, whatever comes with it', () => {
    for (const worst of ['agent_replaced', 'overdue', 'failing'] as const) {
      expect(bannerTone([worst])).toBe('error')
      expect(bannerTone([worst, 'local_storage'])).toBe('error')
    }
  })
})
