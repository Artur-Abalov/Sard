// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import openapi from './api/openapi.json'
import en from './locales/en.json'
import ru from './locales/ru.json'

const dictionaries = { ru, en }

function has(dictionary: object, path: string): boolean {
  let node: unknown = dictionary
  for (const part of path.split('.')) {
    if (typeof node !== 'object' || node === null || !(part in node)) return false
    node = (node as Record<string, unknown>)[part]
  }
  return typeof node === 'string' && node !== ''
}

const schemas = openapi.components.schemas as unknown as Record<string, { enum: string[] }>

const J1_TEXTS = [
  'run.cancel.button',
  'run.cancel.title',
  'run.cancel.backupText',
  'run.cancel.restoreWarning',
  'run.cancel.confirm',
  'run.cancellation',
  'run.cancelledBy',
  'run.waitingUntil',
  'run.timedOut.storage',
  'run.timedOut.increase',
  'run.timedOut.openSource',
  'run.agentUnavailableSince',
  'run.agentNeverConnected',
  'run.lateStatus',
  'runs.waitingUntil',
  'form.backupTimeout',
  'form.restoreTimeout',
  'form.timeoutDefault',
  'settings.runDefaults.title',
  'settings.runDefaults.backup',
  'settings.runDefaults.restore',
  'settings.runDefaults.submit',
  'settings.runDefaults.saved',
  'errors.run_finished',
  'verification.outcome.cancelled',
  'verification.tab.job.cancelling',
  'verification.tab.cancel.button',
  'verification.tab.cancel.title',
  'verification.tab.cancel.text',
  'verification.tab.cancel.confirm',
  'verification.tab.cancel.finished',
  'verification.tab.cancelledBy',
]

describe.each(Object.entries(dictionaries))('the %s dictionary', (_, dictionary) => {
  test.each(['RunOutcome', 'RunStatus', 'StepStatus'])('names every value of %s', (group) => {
    for (const value of schemas[group].enum) {
      expect(has(dictionary, `enum.${group}.${value}`), `${group}.${value}`).toBe(true)
    }
  })

  test.each(J1_TEXTS)('has the text %s', (path) => {
    expect(has(dictionary, path)).toBe(true)
  })
})
