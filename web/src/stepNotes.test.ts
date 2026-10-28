// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import {
  backupExplanation,
  incompleteMarkShown,
  snapshotNoteKey,
  unreadableRemainder,
  stepNoteShown,
  waitsForAgent,
} from './stepNotes'

describe('the mark that a run is incomplete', () => {
  test.each([
    ['failed', true, true],
    ['failed', false, false],
    ['succeeded', false, false],
    ['running', false, false],
    ['succeeded', true, true],
  ] as const)('a %s run with partial %s: %s', (_status, partial, shown) => {
    expect(incompleteMarkShown({ partial })).toBe(shown)
  })
})

describe('the explanation of an incomplete snapshot', () => {
  test.each([
    ['unreadable_files', 'unreadable_files', true, false],
    ['snapshot_set_incomplete', 'snapshot_set_incomplete', false, true],
    ['unknown', 'unknown', false, true],
    [null, null, false, true],
  ] as const)('reason %s: note %s, hint %s, message shown %s', (reason, note, hint, message) => {
    expect(snapshotNoteKey(reason)).toBe(note === null ? null : `incomplete.${note}`)
    expect(backupExplanation({ incompleteReason: reason })).toEqual({
      noteKey: note === null ? null : `incomplete.${note}`,
      permissionHint: hint,
      messageShown: message,
    })
  })

  test('no backup output explains nothing and shows the message', () => {
    expect(backupExplanation(null)).toEqual({
      noteKey: null,
      permissionHint: false,
      messageShown: true,
    })
  })
})

describe('the unreadable files beyond the listed paths', () => {
  test.each([
    [11, 10, 1],
    [2, 2, null],
    [3, 0, null],
    [null, 0, null],
    [1, 3, null],
  ] as const)('count %s, %s paths: %s', (count, paths, rest) => {
    expect(unreadableRemainder(count, paths)).toBe(rest)
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
