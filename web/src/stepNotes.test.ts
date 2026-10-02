// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { partialNoteShown, stepNoteShown, waitsForAgent } from './stepNotes'

const backup = (partial: boolean) => ({
  snapshotId: 'a1b2c3',
  totalBytes: 1,
  addedBytes: 1,
  repositoryId: 'r',
  partial,
})

describe('the note that a snapshot is incomplete', () => {
  test.each([
    ['failed', backup(true), true],
    ['failed', backup(false), false],
    ['lost', backup(true), true],
    ['succeeded', backup(false), false],
    ['failed', null, false],
  ] as const)('a %s step with backup %j: %s', (status, output, shown) => {
    expect(partialNoteShown({ status, backup: output })).toBe(shown)
  })
})

describe('the sentence before the reason of a step', () => {
  test.each([
    ['lost', true],
    ['rejected', true],
    ['failed', false],
    ['timed_out', false],
    ['succeeded', false],
  ] as const)('a %s step: %s', (status, shown) => {
    expect(stepNoteShown(status)).toBe(shown)
  })
})

describe('the note that a run waits for its agent', () => {
  test.each([
    ['queued', 'offline', true],
    ['queued', 'online', false],
    ['queued', undefined, false],
    ['running', 'offline', false],
    ['failed', 'offline', false],
  ] as const)('a %s run, the agent %s: %s', (run, agent, shown) => {
    expect(waitsForAgent(run, agent)).toBe(shown)
  })
})
