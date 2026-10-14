// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { onboardingBanner, screenOf, stepMarks, type Onboarding } from './state'

type State = Onboarding['steps'][number]['state']
type StepStates = [State, State] | [State, State, State, State]

function onboarding(
  access: Onboarding['access'],
  [ca, admin, selfBackup = 'upcoming', keys = 'upcoming']: StepStates,
  setupCode: Onboarding['setupCode'] = 'active',
  restriction: Onboarding['restriction'] = 'full',
): Onboarding {
  return {
    access,
    setupCode,
    ca: null,
    caReplaceable: null,
    restriction,
    steps: [
      { id: 'ca', state: ca },
      { id: 'admin', state: admin },
      { id: 'self_backup', state: selfBackup },
      { id: 'keys_confirmed', state: keys },
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

describe('screenOf with an administrator session (Рк10, F4b)', () => {
  test.each([
    ['pending', 'upcoming', 'selfBackup'],
    ['done', 'pending', 'keys'],
  ] as const)(
    'self_backup %s and keys_confirmed %s show the %s screen',
    (selfBackup, keys, screen) => {
      expect(screenOf(onboarding('admin', ['done', 'done', selfBackup, keys]))).toBe(screen)
    },
  )

  test('the key copy opens only when the server reports the self-backup step done', () => {
    expect(screenOf(onboarding('admin', ['done', 'done', 'pending', 'upcoming']))).toBe(
      'selfBackup',
    )
  })

  test('all steps done leave nothing to show', () => {
    const done = onboarding('admin', ['done', 'done', 'done', 'done'], 'not_issued', 'none')
    expect(screenOf(done)).toBe('complete')
  })
})

describe('onboardingBanner (Рк16)', () => {
  test('is shown with a link to /setup while only new agents are held back', () => {
    const upgraded = onboarding('admin', ['done', 'done', 'pending', 'upcoming'], 'not_issued')
    expect(onboardingBanner({ ...upgraded, restriction: 'new_agents' })).toEqual({ to: '/setup' })
  })

  test.each(['none', 'full'] as const)('is not shown for the restriction %s', (restriction) => {
    expect(onboardingBanner(onboarding('admin', ['done', 'done'], 'not_issued', restriction))).toBe(
      null,
    )
  })
})

describe('stepMarks (Рк5)', () => {
  test('lists the four steps in the server order, the first pending one current', () => {
    expect(stepMarks(onboarding('setup', ['done', 'pending']))).toEqual([
      { id: 'ca', mark: 'done' },
      { id: 'admin', mark: 'current' },
      { id: 'self_backup', mark: 'todo' },
      { id: 'keys_confirmed', mark: 'todo' },
    ])
  })

  test('no step is announced as "soon": the self-backup and the key copy are required', () => {
    const marks = stepMarks(onboarding('admin', ['done', 'done', 'pending', 'upcoming']))
    expect(marks.map((s) => s.mark)).toEqual(['done', 'done', 'current', 'todo'])
  })

  test('a pending step after the current one is only to do', () => {
    expect(stepMarks(onboarding('none', ['pending', 'pending'])).map((s) => s.mark)).toEqual([
      'current',
      'todo',
      'todo',
      'todo',
    ])
  })
})
