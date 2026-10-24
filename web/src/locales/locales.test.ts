// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { emptyForm } from '../onboarding/selfBackup'
import en from './en.json'
import ru from './ru.json'

// Nested key paths, e.g. "login.wrongPassword", so a value moved to the wrong
// section is caught too, not just a missing leaf.
function keyPaths(value: unknown, prefix = ''): string[] {
  if (typeof value !== 'object' || value === null) return [prefix]
  return Object.entries(value).flatMap(([key, child]) =>
    keyPaths(child, prefix === '' ? key : `${prefix}.${key}`),
  )
}

describe('locales', () => {
  test('ru and en declare the same set of keys', () => {
    expect(new Set(keyPaths(ru))).toEqual(new Set(keyPaths(en)))
  })
})

describe('certificate texts', () => {
  const paths = [
    'agents.certificate',
    'agent.certificateNotAfter',
    'agent.certificateDaysLeft',
    'agent.certificateState',
    'agent.certificateRenewalIssued',
    'agent.certificateExpiringHint',
    'agent.certificateExpiredHint',
  ]

  test('the new keys exist in both dictionaries', () => {
    for (const dictionary of [ru, en]) {
      const present = new Set(keyPaths(dictionary))
      for (const path of paths) expect(present.has(path), path).toBe(true)
    }
  })

  test('the hints do not contain commands', () => {
    for (const dictionary of [ru, en]) {
      const { agent } = dictionary
      for (const text of [agent.certificateExpiringHint, agent.certificateExpiredHint]) {
        expect(text).not.toContain('sard-agent')
      }
    }
  })
})

describe('the labels of the self-backup storage form', () => {
  const fields = Object.keys(emptyForm).filter((field) => !['storage', 'provider'].includes(field))

  test('every field of the form has a label in both dictionaries', () => {
    for (const dictionary of [ru, en]) {
      const present = new Set(keyPaths(dictionary))
      for (const field of fields) {
        expect(present.has(`setup.selfBackup.field.${field}`), field).toBe(true)
      }
    }
  })
})
