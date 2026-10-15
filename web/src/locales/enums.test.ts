// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import spec from '../api/openapi.json'
import en from './en.json'
import ru from './ru.json'

const schemas = spec.components.schemas as unknown as Record<string, { enum: string[] }>

// A key path such as "enum.RunStatus.queued" in a dictionary; undefined when absent.
function lookup(dictionary: unknown, path: string): unknown {
  return path
    .split('.')
    .reduce<unknown>(
      (node, key) =>
        typeof node === 'object' && node !== null ? Reflect.get(node, key) : undefined,
      dictionary,
    )
}

const enums = [
  'RunStatus',
  'StepStatus',
  'StepPhase',
  'EnrollmentTokenStatus',
  'AgentStatus',
  'LogLevel',
  'StepAction',
  'RunTrigger',
  'RunKind',
  'AgentRole',
]

describe('every value of the contract is translated', () => {
  test.each(enums)('%s has a non-empty string in both languages', (schema) => {
    for (const value of schemas[schema].enum) {
      for (const dictionary of [ru, en]) {
        const text = lookup(dictionary, `enum.${schema}.${value}`)
        expect(typeof text === 'string' && text !== '', `${schema}.${value}`).toBe(true)
      }
    }
  })

  test('ErrorCode has a message under errors.<code> in both languages', () => {
    for (const code of schemas.ErrorCode.enum) {
      for (const dictionary of [ru, en]) {
        const text = lookup(dictionary, `errors.${code}`)
        expect(typeof text === 'string' && text !== '', code).toBe(true)
      }
    }
  })

  test('SelfBackupWarningCode has a message under selfBackup.warning.<code> in both languages', () => {
    for (const code of schemas.SelfBackupWarningCode.enum) {
      for (const dictionary of [ru, en]) {
        const text = lookup(dictionary, `selfBackup.warning.${code}`)
        expect(typeof text === 'string' && text !== '', code).toBe(true)
      }
    }
  })

  test.each([
    ['RestoreWarningCode', 'restore.warning'],
    ['IneligibleReason', 'restore.ineligible'],
    ['RestoreBlockedReason', 'restore.blocked'],
  ])('%s has a message under %s.<value> in both languages', (schema, prefix) => {
    for (const value of schemas[schema].enum) {
      for (const dictionary of [ru, en]) {
        const text = lookup(dictionary, `${prefix}.${value}`)
        expect(typeof text === 'string' && text !== '', value).toBe(true)
      }
    }
  })

  test('the codes of F4b are translated: onboarding_incomplete, first_backup_pending, keys_unconfirmed', () => {
    for (const path of [
      'errors.onboarding_incomplete',
      'errors.first_backup_pending',
      'selfBackup.warning.keys_unconfirmed',
    ]) {
      for (const dictionary of [ru, en]) {
        expect(typeof lookup(dictionary, path), path).toBe('string')
      }
    }
  })
})
