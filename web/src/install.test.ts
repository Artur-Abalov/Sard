// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import type { components } from './api/schema'
import { stepText, withEnrollCommand } from './install'

type Step = components['schemas']['InstallStep']

const steps: Step[] = [
  { kind: 'download', commands: ['curl -fsSLO a', 'curl -fsSLO b'], optional: false },
  {
    kind: 'enroll',
    commands: ['sudo -u sard-agent sard-agent enroll --token <TOKEN>'],
    optional: false,
  },
  { kind: 'start', commands: ['sudo systemctl enable --now sard-agent.service'], optional: false },
]

describe('the text a step copies', () => {
  test('is exactly its commands, one to a line', () => {
    expect(stepText(steps[0])).toBe('curl -fsSLO a\ncurl -fsSLO b')
  })

  test('of a step with one command is that command', () => {
    expect(stepText(steps[2])).toBe('sudo systemctl enable --now sard-agent.service')
  })
})

describe('the steps of the token dialog', () => {
  const enrollCommand =
    'sudo -u sard-agent sard-agent enroll --server sard.example.com:9090 --token sard_x.y'

  test('keep the order and every step but enroll as the server sent them', () => {
    const shown = withEnrollCommand(steps, enrollCommand)
    expect(shown.map((step) => step.kind)).toEqual(['download', 'enroll', 'start'])
    expect(shown[0]).toBe(steps[0])
    expect(shown[2]).toBe(steps[2])
  })

  test('show the enrollCommand of the created token in place of the enroll step', () => {
    expect(withEnrollCommand(steps, enrollCommand)[1]).toEqual({
      kind: 'enroll',
      commands: [enrollCommand],
      optional: false,
    })
  })

  test('are the same steps when there is no enroll step (downloads are off)', () => {
    expect(withEnrollCommand([], enrollCommand)).toEqual([])
  })
})
