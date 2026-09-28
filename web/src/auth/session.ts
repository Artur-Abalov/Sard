// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { client } from '../api/client'
import type { components } from '../api/schema'

export type Session = components['schemas']['Session']

/** No session, or it expired: GET /api/v1/session answered 401. */
export class UnauthenticatedError extends Error {}

/** The current session, or throws (UnauthenticatedError for 401, Error otherwise). */
export async function checkSession(): Promise<Session> {
  const { data, response } = await client.GET('/api/v1/session')
  if (response.status === 401) {
    throw new UnauthenticatedError()
  }
  if (data === undefined) {
    throw new Error(`GET /api/v1/session failed: ${response.status}`)
  }
  return data
}

/** Signs in with [password]; the response carries the outcome (204/401/429) — see Login.tsx. */
export async function signIn(password: string): Promise<Response> {
  const { response } = await client.POST('/api/v1/session', { body: { password } })
  return response
}

/** Ends the session; the response carries the outcome (204/401) — see Layout.tsx's logout button. */
export async function signOut(): Promise<Response> {
  const { response } = await client.DELETE('/api/v1/session')
  return response
}
