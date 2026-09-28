// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { http } from '../mocks/http'
import { server } from '../mocks/node'
import { checkSession, UnauthenticatedError } from './session'

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
