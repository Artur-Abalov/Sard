// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { redirectIfSignedIn, setupPending, setupRedirect } from './auth/loginGuard'
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

const withAdminStep = (state: 'pending' | 'done') =>
  server.use(
    http.get('/api/v1/onboarding', ({ response }) =>
      response(200).json({
        access: 'none',
        setupCode: 'active',
        ca: null,
        caReplaceable: null,
        restriction: 'full',
        steps: [
          { id: 'ca', state },
          { id: 'admin', state },
          { id: 'self_backup', state: 'upcoming' },
          { id: 'keys_confirmed', state: 'upcoming' },
        ],
      }),
    ),
  )

describe('setupPending (the login route sends a visit to the wizard, Рк2)', () => {
  test('is true while the admin step is pending', async () => {
    withAdminStep('pending')
    await expect(setupPending()).resolves.toBe(true)
  })

  test('is false once the admin step is done', async () => {
    withAdminStep('done')
    await expect(setupPending()).resolves.toBe(false)
  })

  test('is false when the state cannot be read: the login page opens as usual', async () => {
    server.use(http.get('/api/v1/onboarding', () => new Response(null, { status: 503 })))
    await expect(setupPending()).resolves.toBe(false)
  })
})

const signedIn = () =>
  server.use(
    http.get('/api/v1/session', ({ response }) =>
      response(200).json({
        tenantId: '00000000-0000-0000-0000-000000000001',
        expiresAt: new Date().toISOString(),
      }),
    ),
  )
const signedOut = () =>
  server.use(http.get('/api/v1/session', ({ response }) => response(401).json(noSession, PROBLEM)))
const withRestriction = (restriction: 'full' | 'new_agents' | 'none') =>
  server.use(
    http.get('/api/v1/onboarding', ({ response }) =>
      response(200).json({
        access: 'admin',
        setupCode: 'not_issued',
        ca: null,
        caReplaceable: false,
        restriction,
        steps: [
          { id: 'ca', state: 'done' },
          { id: 'admin', state: 'done' },
          { id: 'self_backup', state: restriction === 'none' ? 'done' : 'pending' },
          { id: 'keys_confirmed', state: restriction === 'none' ? 'done' : 'upcoming' },
        ],
      }),
    ),
  )

describe('setupRedirect (the wizard route, Рк2, Рк10)', () => {
  test('stays on the wizard while the admin step is pending', async () => {
    withAdminStep('pending')
    await expect(setupRedirect()).resolves.toBeNull()
  })

  test('sends a visit without a session to sign-in once the admin step is done', async () => {
    withAdminStep('done')
    signedOut()
    await expect(setupRedirect()).resolves.toBe('/login')
  })

  test('sends a signed-in visit home once the onboarding is complete', async () => {
    signedIn()
    withRestriction('none')
    await expect(setupRedirect()).resolves.toBe('/')
  })

  test.each(['full', 'new_agents'] as const)(
    'keeps a signed-in visit on the wizard while the restriction is %s',
    async (restriction) => {
      signedIn()
      withRestriction(restriction)
      await expect(setupRedirect()).resolves.toBeNull()
    },
  )

  test('stays on the wizard when the state cannot be read: it shows the failure itself', async () => {
    server.use(http.get('/api/v1/onboarding', () => new Response(null, { status: 503 })))
    await expect(setupRedirect()).resolves.toBeNull()
  })
})
