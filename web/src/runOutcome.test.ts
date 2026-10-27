// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import type { components } from './api/schema'
import { cancellable, timeoutOfRun } from './runOutcome'

type Outcome = components['schemas']['RunOutcome']

describe('the cancel button', () => {
  test.each([
    ['queued', true],
    ['waiting_for_agent', true],
    ['dispatched', true],
    ['running', true],
    ['cancelling', false],
    ['succeeded', false],
    ['failed', false],
    ['timed_out', false],
    ['rejected', false],
    ['lost', false],
    ['cancelled', false],
    ['cancel_unconfirmed', false],
    ['agent_unavailable', false],
  ] as [Outcome, boolean][])('outcome %s: button %s', (outcome, shown) => {
    expect(cancellable({ outcome, trigger: 'manual' })).toBe(shown)
  })

  test('a restore verification has no button before J1f', () => {
    expect(cancellable({ outcome: 'running', trigger: 'verification' })).toBe(false)
  })
})

describe('the timeout of a run that timed out', () => {
  test('is the timeout of its step that timed out', () => {
    const steps = [
      { status: 'succeeded', timeoutSeconds: 60 },
      { status: 'timed_out', timeoutSeconds: 21600 },
    ] as const
    expect(timeoutOfRun(steps)).toBe(21600)
  })

  test('is unknown when no step timed out or the step has none', () => {
    expect(timeoutOfRun([{ status: 'failed', timeoutSeconds: 60 }])).toBeNull()
    expect(timeoutOfRun([{ status: 'timed_out', timeoutSeconds: null }])).toBeNull()
  })
})
