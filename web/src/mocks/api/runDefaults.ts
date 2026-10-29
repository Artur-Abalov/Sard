// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { http } from '../http'
import { noSession, PROBLEM } from '../problems'
import { state } from '../state'

const MAX_MINUTES = 1440

const valid = (minutes: number) =>
  Number.isInteger(minutes) && minutes >= 1 && minutes <= MAX_MINUTES

// GET and PUT /api/v1/run-defaults by the rules of docs/specs/server/run-cancel-deadlines.feature (J1b).
export const runDefaultsHandlers = [
  http.get('/api/v1/run-defaults', ({ response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    return response(200).json(state.runDefaults)
  }),

  http.put('/api/v1/run-defaults', async ({ request, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const input = await request.json()
    const errors = (['backupTimeoutMinutes', 'restoreTimeoutMinutes'] as const)
      .filter((field) => !valid(input[field]))
      .map((field) => ({ field, message: 'must be 1 to 1440 minutes' }))
    if (errors.length > 0) {
      return response(422).json(
        {
          type: 'about:blank',
          title: 'Unprocessable',
          status: 422,
          detail: null,
          code: 'validation_failed',
          errors,
        },
        PROBLEM,
      )
    }
    state.runDefaults = input
    return response(200).json(state.runDefaults)
  }),
]
