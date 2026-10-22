// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { bannerRepositoryCount } from './recoveryKeyBanner'

// docs/specs/web/restic-keys.feature, КВ3: the console only compares the server's number with zero.
describe('bannerRepositoryCount', () => {
  test('is the number of the server when it is above zero', () => {
    expect(bannerRepositoryCount({ withoutRecoveryKey: 3 })).toBe(3)
  })

  test('is null for zero, so there is no banner', () => {
    expect(bannerRepositoryCount({ withoutRecoveryKey: 0 })).toBeNull()
  })

  test('is null without an answer (loading or a failed request)', () => {
    expect(bannerRepositoryCount(undefined)).toBeNull()
  })
})
