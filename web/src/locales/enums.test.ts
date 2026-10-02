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
})
