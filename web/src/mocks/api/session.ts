// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { TENANT_ID } from '../fixtures'
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

const IDLE_HOURS = 12

export const SESSION_COOKIE = 'sard_session=mock; HttpOnly; SameSite=Strict; Path=/'

export const sessionHandlers = [
  http.post('/api/v1/session', async ({ request, response }) => {
    const now = Date.now()
    if (!state.onboarding.adminDone) {
      return response(409).json(problem(409, 'Conflict', 'setup_required'), PROBLEM)
    }
    const retryAfter = retryAfterSeconds(state.signIns, now)
    if (retryAfter !== null) {
      return response(429).json(tooManyAttempts, lockedInit(retryAfter))
    }
    const { password } = await request.json()
    if (password !== state.password) {
      recordFailure(state.signIns, now)
      return response(401).json(noSession, PROBLEM)
    }
    state.signedIn = true
    forgetFailures(state.signIns)
    return response(204).empty({ headers: { 'Set-Cookie': SESSION_COOKIE } })
  }),

  http.get('/api/v1/session', ({ response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    return response(200).json({
      tenantId: TENANT_ID,
      expiresAt: new Date(Date.now() + IDLE_HOURS * 3_600_000).toISOString(),
    })
  }),

  http.delete('/api/v1/session', ({ response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    state.signedIn = false
    return response(204).empty()
  }),
]
