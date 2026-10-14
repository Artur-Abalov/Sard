// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import en from './locales/en.json'
import ru from './locales/ru.json'
import keysScreen from './pages/setup/KeysScreen.tsx?raw'

// docs/specs/web/onboarding-self-backup.feature, "Тексты шага копии ключей берутся только из ключей setup keys" (Рк14):
// replacing the step by "issue a recovery key" (V3) changes the dictionaries, not the state or the requests.
function lookup(dictionary: unknown, path: string): unknown {
  return path
    .split('.')
    .reduce<unknown>(
      (node, key) =>
        typeof node === 'object' && node !== null ? Reflect.get(node, key) : undefined,
      dictionary,
    )
}

const usedKeys = [...keysScreen.matchAll(/\bt\(\s*['"]([^'"]+)['"]/g)].map((match) => match[1])

describe('the text of the key copy step', () => {
  test('the component uses text keys', () => {
    expect(usedKeys.length).toBeGreaterThan(5)
  })

  test.each([
    ['ru', ru],
    ['en', en],
  ] as const)(
    'every string of the step lies under setup.keys in the %s dictionary',
    (_language, dictionary) => {
      for (const key of usedKeys) {
        expect(key.startsWith('setup.keys.'), key).toBe(true)
        expect(typeof lookup(dictionary, key), key).toBe('string')
      }
    },
  )

  test('the component holds no literal text', () => {
    const code = keysScreen.replace(/\/\/.*$/gm, '').replaceAll('=>', '→')
    const literals = [...code.matchAll(/>\s*([^<>{}\s][^<>{}]*)</g)].map((match) => match[1].trim())
    expect(literals.filter((text) => /\p{L}/u.test(text))).toEqual([])
  })
})
