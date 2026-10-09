// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import {
  POLL_INTERVAL_MS,
  runListPollInterval,
  runPollInterval,
  schedulePollInterval,
  stepPollInterval,
  tokenPollInterval,
} from './polling'

describe('the polling decision', () => {
  test('the interval is three seconds', () => {
    expect(POLL_INTERVAL_MS).toBe(3000)
  })

  test.each([
    ['queued', POLL_INTERVAL_MS],
    ['dispatched', POLL_INTERVAL_MS],
    ['running', POLL_INTERVAL_MS],
    ['succeeded', false],
    ['failed', false],
    ['cancelled', false],
  ] as const)('a run in %s: %s', (status, interval) => {
    expect(runPollInterval(status)).toBe(interval)
  })

  test.each([
    ['queued', POLL_INTERVAL_MS],
    ['dispatched', POLL_INTERVAL_MS],
    ['running', POLL_INTERVAL_MS],
    ['succeeded', false],
    ['failed', false],
    ['cancelled', false],
    ['timed_out', false],
    ['rejected', false],
    ['lost', false],
  ] as const)('a step in %s: %s', (status, interval) => {
    expect(stepPollInterval(status)).toBe(interval)
  })

  test('a list of runs is polled while one of them is active', () => {
    expect(runListPollInterval([{ status: 'succeeded' }, { status: 'running' }])).toBe(
      POLL_INTERVAL_MS,
    )
    expect(runListPollInterval([{ status: 'succeeded' }, { status: 'failed' }])).toBe(false)
    expect(runListPollInterval([])).toBe(false)
    expect(runListPollInterval(undefined)).toBe(false)
  })

  test.each([
    ['active', POLL_INTERVAL_MS],
    ['used', false],
    ['expired', false],
    ['revoked', false],
  ] as const)('a token in %s: %s', (status, interval) => {
    expect(tokenPollInterval(status)).toBe(interval)
  })

  test('nothing is known yet: no polling', () => {
    expect(runPollInterval(undefined)).toBe(false)
    expect(stepPollInterval(undefined)).toBe(false)
    expect(tokenPollInterval(undefined)).toBe(false)
  })

  test.each([
    ['queued', POLL_INTERVAL_MS],
    ['running', POLL_INTERVAL_MS],
    ['succeeded', false],
    ['failed', false],
  ] as const)('a schedule whose last run is %s: %s', (status, interval) => {
    expect(schedulePollInterval({ lastRun: { status } })).toBe(interval)
  })

  test('a schedule with no run, or none at all, is not polled', () => {
    expect(schedulePollInterval({ lastRun: null })).toBe(false)
    expect(schedulePollInterval(undefined)).toBe(false)
  })
})
