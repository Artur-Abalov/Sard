// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { screenOf, stepMarks, type Onboarding } from './state'

type StepStates = [Onboarding['steps'][number]['state'], Onboarding['steps'][number]['state']]

function onboarding(
  access: Onboarding['access'],
  [ca, admin]: StepStates,
  setupCode: Onboarding['setupCode'] = 'active',
): Onboarding {
  return {
    access,
    setupCode,
    ca: null,
    caReplaceable: null,
    steps: [
      { id: 'ca', state: ca },
      { id: 'admin', state: admin },
      { id: 'self_backup', state: 'upcoming' },
      { id: 'keys_confirmed', state: 'upcoming' },
    ],
  }
}

describe('screenOf (Рк3, Рк4)', () => {
  test('without a session and with an active code the wizard asks for the code', () => {
    expect(screenOf(onboarding('none', ['pending', 'pending']))).toBe('code')
  })

  test.each(['expired', 'not_issued'] as const)(
    'without a session and with a %s code the wizard asks to restart the server',
    (code) => {
      expect(screenOf(onboarding('none', ['pending', 'pending'], code))).toBe('restart')
    },
  )

  test('a setup session with the CA step pending shows the CA step', () => {
    expect(screenOf(onboarding('setup', ['pending', 'pending']))).toBe('ca')
  })

  test('a setup session with the CA step done shows the admin step', () => {
    expect(screenOf(onboarding('setup', ['done', 'pending']))).toBe('admin')
  })
})

describe('stepMarks (Рк5)', () => {
  test('lists the four steps in the server order, the first pending one current', () => {
    expect(stepMarks(onboarding('setup', ['done', 'pending']))).toEqual([
      { id: 'ca', mark: 'done' },
      { id: 'admin', mark: 'current' },
      { id: 'self_backup', mark: 'soon' },
      { id: 'keys_confirmed', mark: 'soon' },
    ])
  })

  test('a pending step after the current one is only to do', () => {
    expect(stepMarks(onboarding('none', ['pending', 'pending'])).map((s) => s.mark)).toEqual([
      'current',
      'todo',
      'soon',
      'soon',
    ])
  })
})
