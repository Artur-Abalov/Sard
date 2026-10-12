// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { bannerLink, bannerTone } from './selfBackupBanner'

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

// F4b (Рк17, OQ-206): an unconfirmed key copy is a lasting setting too, and it has a place of its own to go.
describe('the warning keys_unconfirmed', () => {
  test('is a warning, not an error, and points to the settings', () => {
    expect(bannerTone(['keys_unconfirmed'])).toBe('warning')
    expect(bannerLink(['keys_unconfirmed'])).toBe('/settings')
  })

  test('stays a warning beside the other settings', () => {
    expect(bannerTone(['local_storage', 'keys_unconfirmed'])).toBe('warning')
  })

  test('a broken backup is still an error beside it', () => {
    expect(bannerTone(['failing', 'keys_unconfirmed'])).toBe('error')
  })

  test('no link without that warning', () => {
    expect(bannerLink([])).toBeNull()
    expect(bannerLink(['failing', 'local_storage'])).toBeNull()
  })
})
