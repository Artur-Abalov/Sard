// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { redirectIfSignedIn } from './auth/loginGuard'
import { http } from './mocks/http'
import { noSession, PROBLEM } from './mocks/problems'
import { server } from './mocks/node'

describe('redirectIfSignedIn (the login route beforeLoad)', () => {
  test('no session lets the login page open', async () => {
    server.use(
      http.get('/api/v1/session', ({ response }) => response(401).json(noSession, PROBLEM)),
    )
    await expect(redirectIfSignedIn(undefined)).resolves.toBeNull()
  })

  test('a signed-in visit resolves the redirect target', async () => {
    server.use(
      http.get('/api/v1/session', ({ response }) =>
        response(200).json({
          tenantId: '00000000-0000-0000-0000-000000000001',
          expiresAt: new Date().toISOString(),
        }),
      ),
    )
    await expect(redirectIfSignedIn('%2Fruns')).resolves.toBe('/')
    await expect(redirectIfSignedIn('/runs')).resolves.toBe('/runs')
    await expect(redirectIfSignedIn(undefined)).resolves.toBe('/')
  })

  test('a session check failure other than 401 still lets the login page open', async () => {
    server.use(http.get('/api/v1/session', () => new Response(null, { status: 500 })))
    await expect(redirectIfSignedIn(undefined)).resolves.toBeNull()
  })
})
