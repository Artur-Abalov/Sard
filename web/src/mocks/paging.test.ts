// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { logLines } from './fixtures'
import { logPage, pageOf } from './paging'

describe('pageOf', () => {
  const items = ['a', 'b', 'c', 'd', 'e']

  test('the first page and a cursor to the next', () => {
    expect(pageOf(items, null, 2)).toEqual({ items: ['a', 'b'], nextCursor: '2' })
  })

  test('the cursor continues where the previous page ended', () => {
    expect(pageOf(items, '2', 2)).toEqual({ items: ['c', 'd'], nextCursor: '4' })
  })

  test('the last page has no cursor', () => {
    expect(pageOf(items, '4', 2)).toEqual({ items: ['e'], nextCursor: null })
    expect(pageOf(items, null, 5)).toEqual({ items, nextCursor: null })
  })
})

describe('logPage', () => {
  const lines = logLines(5)

  test('lines after afterSeq, at most limit', () => {
    const page = logPage(lines, 1, 2)
    expect(page.items.map((l) => l.seq)).toEqual([2, 3])
    expect(page).toMatchObject({ nextAfterSeq: 3, hasMore: true })
  })

  test('the tail says there is no more for now', () => {
    const page = logPage(lines, 3, 10)
    expect(page.items.map((l) => l.seq)).toEqual([4, 5])
    expect(page).toMatchObject({ nextAfterSeq: 5, hasMore: false })
  })

  test('past the end: no lines, the same afterSeq to poll again', () => {
    expect(logPage(lines, 5, 10)).toEqual({ items: [], nextAfterSeq: 5, hasMore: false })
  })

  test('exactly limit lines left is the end', () => {
    expect(logPage(lines, 3, 2)).toMatchObject({ nextAfterSeq: 5, hasMore: false })
  })
})
