// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { formatLockoutMinutes } from '../auth/lockout'

// What the server answered, reduced to what the wizard and the settings form decide on.
export interface Answer {
  status: number
  // the machine code of a problem body, null for an empty or foreign body
  code: string | null
  // Retry-After in seconds, 0 when absent
  retryAfter: number
}

export function answerFrom(status: number, retryAfter: string | null, body: unknown): Answer {
  const code =
    typeof body === 'object' && body !== null && 'code' in body && typeof body.code === 'string'
      ? body.code
      : null
  const seconds = Number(retryAfter ?? '0')
  return { status, code, retryAfter: Number.isFinite(seconds) ? seconds : 0 }
}

// A message to show: a key of src/locales, the lock time in whole minutes where it has one.
export interface Notice {
  key: string
  minutes?: number
}

// What a form does with an answer: the step is done, it goes elsewhere (to the code screen,
// to the CA step, to sign-in), or it stays and shows why.
export type StepResult =
  | { kind: 'done' }
  | { kind: 'goto'; target: 'code' | 'ca' | 'login'; notice: Notice | null }
  | { kind: 'stay'; notice: Notice }

const done: StepResult = { kind: 'done' }
const stay = (key: string, minutes?: number): StepResult => ({
  kind: 'stay',
  notice: minutes === undefined ? { key } : { key, minutes },
})
const toLogin = (notice: Notice | null): StepResult => ({ kind: 'goto', target: 'login', notice })
const sessionEnded: StepResult = {
  kind: 'goto',
  target: 'code',
  notice: { key: 'setup.sessionEnded' },
}

export function codeResult(answer: Answer): StepResult {
  switch (answer.status) {
    case 204:
      return done
    case 401:
      return stay('setup.code.invalid')
    case 429:
      return stay('setup.code.locked', formatLockoutMinutes(answer.retryAfter))
    case 409:
      return toLogin({ key: 'setup.completed' })
    default:
      return stay('setup.unavailable')
  }
}

export function caStepResult(answer: Answer): StepResult {
  switch (answer.status) {
    case 204:
      return done
    case 401:
      return sessionEnded
    case 409:
      return toLogin({ key: 'setup.completed' })
    default:
      return stay('setup.unavailable')
  }
}

function adminConflict(answer: Answer): StepResult {
  return answer.code === 'ca_step_pending'
    ? { kind: 'goto', target: 'ca', notice: null }
    : toLogin({ key: 'setup.completed' })
}

export function adminStepResult(answer: Answer): StepResult {
  switch (answer.status) {
    case 204:
      return done
    case 401:
      return sessionEnded
    case 409:
      return adminConflict(answer)
    case 422:
      return stay('setup.admin.requirements')
    default:
      return stay('setup.unavailable')
  }
}

export function passwordChangeResult(answer: Answer): StepResult {
  switch (answer.status) {
    case 204:
      return done
    case 401:
      return toLogin(null)
    case 422:
      return stay(
        answer.code === 'wrong_password' ? 'settings.wrongPassword' : 'settings.requirements',
      )
    case 429:
      return stay('settings.locked', formatLockoutMinutes(answer.retryAfter))
    case 501:
      return stay('settings.notSupported')
    default:
      return stay('settings.unavailable')
  }
}
