// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { mergeLogLines } from './logs'

const line = (seq: number) => ({ seq, time: null, level: 'info' as const, text: `line ${seq}` })
const range = (from: number, to: number) =>
  Array.from({ length: to - from + 1 }, (_, i) => line(from + i))

describe('log lines', () => {
  test('lines received again are not shown twice', () => {
    expect(mergeLogLines(range(1, 5), range(4, 7)).map((l) => l.seq)).toEqual([1, 2, 3, 4, 5, 6, 7])
  })

  test('a page after the last line is appended', () => {
    expect(mergeLogLines(range(1, 2), range(3, 4))).toEqual(range(1, 4))
  })

  test('the lines stay in seq order whatever order the pages came in', () => {
    expect(mergeLogLines(range(3, 4), range(1, 2)).map((l) => l.seq)).toEqual([1, 2, 3, 4])
  })

  test('nothing new changes nothing', () => {
    const shown = range(1, 3)

    expect(mergeLogLines(shown, [])).toEqual(shown)
  })
})
