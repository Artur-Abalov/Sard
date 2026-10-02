// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'

// Visual design lives in src/theme.ts only: components name a tone, never a color.
const LITERAL_COLOR = /\b(?:color|c|bg)=["'](?!dimmed\b)[a-z]+(?:\.\d)?["']/g

const sources = import.meta.glob<string>('./**/*.tsx', {
  query: '?raw',
  import: 'default',
  eager: true,
})

describe('visual design', () => {
  test('the glob finds the components', () => {
    expect(Object.keys(sources).length).toBeGreaterThan(20)
  })

  test('no component names a color literally', () => {
    const offenders = Object.entries(sources).flatMap(([file, text]) =>
      (text.match(LITERAL_COLOR) ?? []).map((hit) => `${file}: ${hit}`),
    )

    expect(offenders).toEqual([])
  })
})
