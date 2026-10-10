// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { queryOptions } from '@tanstack/react-query'
import { call } from '../api/call'
import { client } from '../api/client'
import { answerFrom, type Answer } from './outcomes'
import type { Onboarding } from './state'

/** GET /api/v1/onboarding; throws on any failure (the callers decide what a failure means). */
export async function fetchOnboarding(): Promise<Onboarding> {
  const { data, response } = await client.GET('/api/v1/onboarding')
  if (data === undefined) {
    throw new Error(`GET /api/v1/onboarding failed: ${response.status}`)
  }
  return data
}

export const onboardingQuery = () =>
  queryOptions({
    queryKey: ['onboarding'],
    queryFn: () => call(client.GET('/api/v1/onboarding')),
  })

interface Result {
  error?: unknown
  response: Response
}

// The answer of a write. A request that never got an answer is status 0: not 2xx, so
// every form reports the server as unavailable.
async function answerOf(request: Promise<Result>): Promise<Answer> {
  try {
    const { response, error } = await request
    return answerFrom(response.status, response.headers.get('Retry-After'), error)
  } catch {
    return { status: 0, code: null, retryAfter: 0 }
  }
}

// The code goes to the server exactly as typed: normalizing it is the server's job.
export const enterCode = (code: string) =>
  answerOf(client.POST('/api/v1/onboarding/setup-session', { body: { code } }))

export const confirmCa = () => answerOf(client.POST('/api/v1/onboarding/ca'))

export const setAdminPassword = (password: string) =>
  answerOf(client.POST('/api/v1/onboarding/admin', { body: { password } }))

export const changePassword = (currentPassword: string, newPassword: string) =>
  answerOf(client.PUT('/api/v1/session/password', { body: { currentPassword, newPassword } }))
