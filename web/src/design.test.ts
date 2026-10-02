// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'

// Visual design lives in src/theme.ts only: components name a tone, never a color.
const LITERAL_COLOR = /\b(?:color|c|bg)=["'](?!dimmed\b)[a-z]+(?:\.\d)?["']/g

// A hex or rgb colour, and capitals set by style: both belong to the theme's tokens, not to a component.
const COLOR_VALUE = /#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(/g
const CAPS = /\btt=|textTransform\s*:\s*['"]uppercase/g

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

  test('no component holds a colour value of its own', () => {
    const offenders = Object.entries(sources).flatMap(([file, text]) =>
      (text.match(COLOR_VALUE) ?? []).map((hit) => `${file}: ${hit}`),
    )

    expect(offenders).toEqual([])
  })

  test('no component sets capitals: text is in sentence case', () => {
    const offenders = Object.entries(sources).flatMap(([file, text]) =>
      (text.match(CAPS) ?? []).map((hit) => `${file}: ${hit}`),
    )

    expect(offenders).toEqual([])
  })
})
