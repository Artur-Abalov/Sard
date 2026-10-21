// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import en from './en.json'
import ru from './ru.json'

function lookup(dictionary: unknown, path: string): unknown {
  return path
    .split('.')
    .reduce<unknown>(
      (node, key) =>
        typeof node === 'object' && node !== null ? Reflect.get(node, key) : undefined,
      dictionary,
    )
}

// docs/specs/web/restic-keys.feature, rule "Тексты есть в обеих локалях".
const NEW_KEYS = [
  'banners.noRecoveryKey',
  'repositories.title',
  'repositories.recoveryKey.present',
  'repositories.recoveryKey.absent',
  'repositories.recoveryKey.unknown',
  'verifierSetup.title',
  'verifierSetup.keyAddHint',
  'verifierSetup.repoAddHint',
  'verifierSetup.unsafeName',
  'verifierSetup.empty',
  'setup.keys.recoveryCommand',
  'setup.keys.confirm',
  'recoveryCard.requires',
]

describe('the texts of the restic keys (V3)', () => {
  test.each([
    ['ru', ru],
    ['en', en],
  ] as const)(
    'the %s dictionary holds every new key with a non-empty text',
    (_language, dictionary) => {
      for (const key of NEW_KEYS) {
        const value = lookup(dictionary, key)
        expect(typeof value, key).toBe('string')
        expect((value as string).trim(), key).not.toBe('')
      }
    },
  )

  test.each([
    ['ru', ru],
    ['en', en],
  ] as const)('the banner text of %s takes the number of repositories', (_language, dictionary) => {
    expect(lookup(dictionary, 'banners.noRecoveryKey')).toContain('{{count}}')
  })
})
