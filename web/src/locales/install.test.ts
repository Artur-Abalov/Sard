// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import spec from '../api/openapi.json'
import en from './en.json'
import ru from './ru.json'

const schemas = spec.components.schemas as unknown as Record<string, { enum: string[] }>

function lookup(dictionary: unknown, path: string): unknown {
  return path
    .split('.')
    .reduce<unknown>(
      (node, key) =>
        typeof node === 'object' && node !== null ? Reflect.get(node, key) : undefined,
      dictionary,
    )
}

function isText(value: unknown): boolean {
  return typeof value === 'string' && value !== ''
}

describe('the install block is translated', () => {
  test.each(['title', 'text'])('every kind of a step has a %s in both languages', (part) => {
    for (const kind of schemas.StepKind.enum) {
      for (const dictionary of [ru, en]) {
        expect(isText(lookup(dictionary, `install.steps.${kind}.${part}`)), `${kind}.${part}`).toBe(
          true,
        )
      }
    }
  })

  test('every reason an upgrade has no commands is explained in both languages', () => {
    for (const reason of schemas.UpgradeReason.enum) {
      for (const dictionary of [ru, en]) {
        expect(isText(lookup(dictionary, `install.reason.${reason}`)), reason).toBe(true)
      }
    }
  })

  test('every choice of architecture, format and downloader has a label in both languages', () => {
    const choices = {
      arch: schemas.InstallArch.enum,
      format: schemas.InstallFormat.enum,
      fetch: schemas.FetchTool.enum,
    }
    for (const [group, values] of Object.entries(choices)) {
      for (const value of values) {
        for (const dictionary of [ru, en]) {
          expect(isText(lookup(dictionary, `install.${group}.${value}`)), `${group}.${value}`).toBe(
            true,
          )
        }
      }
    }
  })
})
