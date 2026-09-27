// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { guard } from './guard'

describe('guard', () => {
  test('lets every page open until sign-in exists', () => {
    expect(() => guard({ pathname: '/agents' })).not.toThrow()
  })
})
