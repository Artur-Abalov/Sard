// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../../api/schema'
import { http } from '../http'
import {
  forgetFailures,
  lockedInit,
  recordFailure,
  retryAfterSeconds,
  tooManyAttempts,
} from '../lockout'
import { noSession, PROBLEM, problem } from '../problems'
import { state } from '../state'
import { SESSION_COOKIE } from './session'
import { FINGERPRINT } from './tokens'

type Schemas = components['schemas']

/** The setup code of the mocks (VITE_MOCK_ONBOARDING=1): what the server prints in its log. */
export const MOCK_SETUP_CODE = 'ABCD-EFGH-JKMN-PQRS-TVWX-YZ01-2345'

const SETUP_COOKIE = 'sard_setup=mock; HttpOnly; SameSite=Strict; Path=/api/v1/onboarding'
const CLEARED_SETUP_COOKIE =
  'sard_setup=; Max-Age=0; HttpOnly; SameSite=Strict; Path=/api/v1/onboarding'
const MIN_PASSWORD = 12
const MAX_PASSWORD = 1024

/** The code as the server reads it: case, hyphens and spaces do not matter. */
function normalized(code: string): string {
  return code.replace(/[\s-]/g, '').toUpperCase()
}

/** The server's rule: 12 to 1024 Unicode code points. */
export function passwordLengthOk(password: string | null | undefined): boolean {
  if (typeof password !== 'string') return false
  const length = [...password].length
  return length >= MIN_PASSWORD && length <= MAX_PASSWORD
}

export function passwordProblem(field: string): Schemas['ValidationProblem'] {
  return {
    ...problem(422, 'Unprocessable Entity', 'validation_failed'),
    errors: [{ field, message: 'must be 12 to 1024 characters' }],
  }
}

function describedState(): Schemas['Onboarding'] {
  const { caDone, adminDone, setupSession } = state.onboarding
  const access = state.signedIn ? 'admin' : setupSession ? 'setup' : 'none'
  const known = access !== 'none'
  return {
    steps: [
      { id: 'ca', state: caDone ? 'done' : 'pending' },
      { id: 'admin', state: adminDone ? 'done' : 'pending' },
      { id: 'self_backup', state: 'upcoming' },
      { id: 'keys_confirmed', state: 'upcoming' },
    ],
    setupCode: adminDone ? 'not_issued' : 'active',
    access,
    ca: known
      ? { fingerprint: FINGERPRINT, origin: 'generated', keyPath: '/var/lib/sard/pki/ca/ca.key' }
      : null,
    caReplaceable: known ? !caDone : null,
  }
}

export const onboardingHandlers = [
  http.get('/api/v1/onboarding', ({ response }) => response(200).json(describedState())),

  http.post('/api/v1/onboarding/setup-session', async ({ request, response }) => {
    if (state.onboarding.adminDone) {
      return response(409).json(problem(409, 'Conflict', 'setup_completed'), PROBLEM)
    }
    const now = Date.now()
    const retryAfter = retryAfterSeconds(state.codes, now)
    if (retryAfter !== null) {
      return response(429).json(tooManyAttempts, lockedInit(retryAfter))
    }
    const { code } = await request.json()
    if (typeof code !== 'string' || normalized(code) !== normalized(MOCK_SETUP_CODE)) {
      recordFailure(state.codes, now)
      return response(401).json(noSession, PROBLEM)
    }
    forgetFailures(state.codes)
    state.onboarding.setupSession = true
    return response(204).empty({ headers: { 'Set-Cookie': SETUP_COOKIE } })
  }),

  http.post('/api/v1/onboarding/ca', ({ response }) => {
    if (!state.onboarding.setupSession) return response(401).json(noSession, PROBLEM)
    state.onboarding.caDone = true
    return response(204).empty()
  }),

  http.post('/api/v1/onboarding/admin', async ({ request, response }) => {
    if (state.onboarding.adminDone) {
      return response(409).json(problem(409, 'Conflict', 'setup_completed'), PROBLEM)
    }
    if (!state.onboarding.setupSession) return response(401).json(noSession, PROBLEM)
    if (!state.onboarding.caDone) {
      return response(409).json(problem(409, 'Conflict', 'ca_step_pending'), PROBLEM)
    }
    const { password } = await request.json()
    if (typeof password !== 'string' || !passwordLengthOk(password)) {
      return response(422).json(passwordProblem('password'), PROBLEM)
    }
    state.password = password
    state.onboarding.adminDone = true
    state.onboarding.setupSession = false
    state.signedIn = true
    const headers = new Headers({ 'Set-Cookie': SESSION_COOKIE })
    headers.append('Set-Cookie', CLEARED_SETUP_COOKIE)
    return response(204).empty({ headers })
  }),

  http.put('/api/v1/session/password', async ({ request, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const now = Date.now()
    const retryAfter = retryAfterSeconds(state.signIns, now)
    if (retryAfter !== null) {
      return response(429).json(tooManyAttempts, lockedInit(retryAfter))
    }
    const { currentPassword, newPassword } = await request.json()
    if (currentPassword !== state.password) {
      recordFailure(state.signIns, now)
      const wrong = {
        ...problem(422, 'Unprocessable Entity', 'wrong_password'),
        errors: [{ field: 'currentPassword', message: 'is not the password' }],
      }
      return response(422).json(wrong, PROBLEM)
    }
    if (!passwordLengthOk(newPassword)) {
      return response(422).json(passwordProblem('newPassword'), PROBLEM)
    }
    state.password = newPassword as string
    forgetFailures(state.signIns)
    return response(204).empty({ headers: { 'Set-Cookie': SESSION_COOKIE } })
  }),
]
