// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { MOCK_PASSWORD, TENANT_ID } from '../fixtures'
import { http } from '../http'
import { noSession, PROBLEM, problem } from '../problems'
import { state } from '../state'

/** Failed sign-ins before the lockout, its window and its duration (К4: matches the server). */
export const MAX_FAILED_SIGN_INS = 5
const WINDOW_MS = 15 * 60_000
const LOCKOUT_SECONDS = 900
const IDLE_HOURS = 12

const locked = { headers: { ...PROBLEM.headers, 'Retry-After': String(LOCKOUT_SECONDS) } }

function isLocked(now: number): boolean {
  state.failedSignIns = state.failedSignIns.filter((at) => now - at < WINDOW_MS)
  return state.failedSignIns.length >= MAX_FAILED_SIGN_INS
}

export const sessionHandlers = [
  http.post('/api/v1/session', async ({ request, response }) => {
    const now = Date.now()
    if (isLocked(now)) {
      return response(429).json(problem(429, 'Too Many Requests', 'too_many_attempts'), locked)
    }
    const { password } = await request.json()
    if (password !== MOCK_PASSWORD) {
      state.failedSignIns.push(now)
      return response(401).json(noSession, PROBLEM)
    }
    state.signedIn = true
    state.failedSignIns = []
    return response(204).empty({
      headers: { 'Set-Cookie': 'sard_session=mock; HttpOnly; SameSite=Strict; Path=/' },
    })
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
