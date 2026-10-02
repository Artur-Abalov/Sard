// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import spec from './api/openapi.json'
import { logLevelColors, statusVisual, statusVisuals } from './theme'

function values(schema: string): string[] {
  const schemas = spec.components.schemas as unknown as Record<string, { enum: string[] }>
  return schemas[schema].enum
}

describe('status visuals', () => {
  test.each(['RunStatus', 'StepStatus', 'EnrollmentTokenStatus', 'AgentStatus'])(
    'every value of %s has a color and an icon',
    (schema) => {
      for (const value of values(schema)) {
        expect(Object.keys(statusVisuals), value).toContain(value)
        expect(statusVisual(value).icon, value).not.toBe(statusVisual('no-such-status').icon)
      }
    },
  )

  test.each(['RunStatus', 'StepStatus', 'EnrollmentTokenStatus', 'AgentStatus'])(
    'the values of %s differ by icon, not only by color',
    (schema) => {
      const icons = values(schema).map((value) => statusVisual(value).icon)

      expect(new Set(icons).size).toBe(icons.length)
    },
  )

  test('every log level has a color', () => {
    expect(Object.keys(logLevelColors).sort()).toEqual([...values('LogLevel')].sort())
  })

  test('a value nobody knows is shown plainly', () => {
    expect(statusVisual('constructor')).toEqual(statusVisual('no-such-status'))
  })
})
