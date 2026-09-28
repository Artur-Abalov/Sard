// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { MOCK_PASSWORD } from '../mocks/fixtures'
import { http } from '../mocks/http'
import { server } from '../mocks/node'
import { checkSession, signIn, signOut, UnauthenticatedError } from './session'

describe('checkSession', () => {
  test('401 unauthenticated throws UnauthenticatedError', async () => {
    await expect(checkSession()).rejects.toBeInstanceOf(UnauthenticatedError)
  })

  test('a server error is a plain failure, not UnauthenticatedError', async () => {
    server.use(http.get('/api/v1/session', () => new Response(null, { status: 500 })))
    const error = await checkSession().catch((e: unknown) => e)
    expect(error).not.toBeInstanceOf(UnauthenticatedError)
  })
})

describe('signIn', () => {
  test('the correct password answers 204', async () => {
    expect((await signIn(MOCK_PASSWORD)).status).toBe(204)
  })

  test('a wrong password answers 401', async () => {
    expect((await signIn('nope')).status).toBe(401)
  })
})

describe('signOut', () => {
  test('with a session answers 204', async () => {
    await signIn(MOCK_PASSWORD)
    expect((await signOut()).status).toBe(204)
  })

  test('without a session answers 401', async () => {
    expect((await signOut()).status).toBe(401)
  })
})
