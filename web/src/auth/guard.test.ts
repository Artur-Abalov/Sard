// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { isRedirect } from '@tanstack/react-router'
import { describe, expect, test } from 'vitest'
import { http } from '../mocks/http'
import { noSession, PROBLEM } from '../mocks/problems'
import { server } from '../mocks/node'
import { guard } from './guard'

describe('guard', () => {
  test('a signed-in visit lets the page open', async () => {
    server.use(
      http.get('/api/v1/session', ({ response }) =>
        response(200).json({
          tenantId: '00000000-0000-0000-0000-000000000001',
          expiresAt: new Date().toISOString(),
        }),
      ),
    )
    await expect(guard({ pathname: '/agents', searchStr: '' })).resolves.toBeUndefined()
  })

  test('no session redirects to /login with the destination remembered', async () => {
    server.use(
      http.get('/api/v1/session', ({ response }) => response(401).json(noSession, PROBLEM)),
    )
    const error = await guard({ pathname: '/agents', searchStr: '' }).catch((e: unknown) => e)
    expect(isRedirect(error)).toBe(true)
    expect(error).toMatchObject({ options: { to: '/login', search: { redirect: '/agents' } } })
  })

  test('the destination keeps its query string', async () => {
    server.use(
      http.get('/api/v1/session', ({ response }) => response(401).json(noSession, PROBLEM)),
    )
    const error = await guard({ pathname: '/runs', searchStr: '?status=failed' }).catch(
      (e: unknown) => e,
    )
    expect(error).toMatchObject({ options: { search: { redirect: '/runs?status=failed' } } })
  })

  test('a failure other than 401 is not a redirect', async () => {
    server.use(http.get('/api/v1/session', () => new Response(null, { status: 500 })))
    const error = await guard({ pathname: '/agents', searchStr: '' }).catch((e: unknown) => e)
    expect(isRedirect(error)).toBe(false)
  })

  describe('before the administrator exists (Рк1)', () => {
    const unauthenticated = () =>
      server.use(
        http.get('/api/v1/session', ({ response }) => response(401).json(noSession, PROBLEM)),
      )
    const stepAdmin = (state: 'pending' | 'done') =>
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

    test('step admin pending redirects to /setup without a redirect parameter', async () => {
      unauthenticated()
      stepAdmin('pending')
      const error = await guard({ pathname: '/agents', searchStr: '' }).catch((e: unknown) => e)
      expect(isRedirect(error)).toBe(true)
      expect(error).toMatchObject({ options: { to: '/setup' } })
      expect((error as { options: object }).options).not.toHaveProperty('search')
    })

    test('step admin done redirects to /login with the destination', async () => {
      unauthenticated()
      stepAdmin('done')
      const error = await guard({ pathname: '/agents', searchStr: '' }).catch((e: unknown) => e)
      expect(error).toMatchObject({ options: { to: '/login', search: { redirect: '/agents' } } })
    })

    test('asks only for the session and the onboarding state', async () => {
      unauthenticated()
      stepAdmin('pending')
      const seen: string[] = []
      const listener = ({ request }: { request: Request }) =>
        void seen.push(`${request.method} ${new URL(request.url).pathname}`)
      server.events.on('request:start', listener)
      await guard({ pathname: '/agents', searchStr: '' }).catch(() => undefined)
      server.events.removeListener('request:start', listener)
      expect(seen).toEqual(['GET /api/v1/session', 'GET /api/v1/onboarding'])
    })

    test.each([
      ['a 503', () => new Response(null, { status: 503 })],
      ['a 500', () => new Response(null, { status: 500 })],
      ['a dropped connection', () => Response.error()],
    ])('%s on the onboarding state is no redirect', async (_name, answer) => {
      unauthenticated()
      server.use(http.get('/api/v1/onboarding', answer))
      const error = await guard({ pathname: '/agents', searchStr: '' }).catch((e: unknown) => e)
      expect(error).toBeInstanceOf(Error)
      expect(isRedirect(error)).toBe(false)
    })
  })
})

// F4b (Рк9): a signed-in visit asks the server how far the onboarding has come.
describe('with an administrator session (Рк9, F4b)', () => {
  const signedIn = () =>
    server.use(
      http.get('/api/v1/session', ({ response }) =>
        response(200).json({
          tenantId: '00000000-0000-0000-0000-000000000001',
          expiresAt: new Date().toISOString(),
        }),
      ),
    )
  const restriction = (value: 'full' | 'new_agents' | 'none') =>
    server.use(
      http.get('/api/v1/onboarding', ({ response }) =>
        response(200).json({
          access: 'admin',
          setupCode: 'not_issued',
          ca: null,
          caReplaceable: false,
          restriction: value,
          steps: [
            { id: 'ca', state: 'done' },
            { id: 'admin', state: 'done' },
            { id: 'self_backup', state: value === 'none' ? 'done' : 'pending' },
            { id: 'keys_confirmed', state: value === 'none' ? 'done' : 'upcoming' },
          ],
        }),
      ),
    )

  test('the full restriction sends a protected page to the wizard', async () => {
    signedIn()
    restriction('full')
    const error = await guard({ pathname: '/agents', searchStr: '' }).catch((e: unknown) => e)
    expect(isRedirect(error)).toBe(true)
    expect(error).toMatchObject({ options: { to: '/setup' } })
  })

  test.each(['new_agents', 'none'] as const)(
    'the restriction %s lets the protected page open',
    async (value) => {
      signedIn()
      restriction(value)
      await expect(guard({ pathname: '/agents', searchStr: '' })).resolves.toBeUndefined()
    },
  )

  test('asks for the session and then for the onboarding state', async () => {
    signedIn()
    restriction('none')
    const seen: string[] = []
    const listener = ({ request }: { request: Request }) =>
      void seen.push(new URL(request.url).pathname)
    server.events.on('request:start', listener)
    await guard({ pathname: '/agents', searchStr: '' })
    server.events.removeListener('request:start', listener)
    expect(seen).toEqual(['/api/v1/session', '/api/v1/onboarding'])
  })

  test('an onboarding state that cannot be read is no redirect', async () => {
    signedIn()
    server.use(http.get('/api/v1/onboarding', () => new Response(null, { status: 503 })))
    const error = await guard({ pathname: '/agents', searchStr: '' }).catch((e: unknown) => e)
    expect(error).toBeInstanceOf(Error)
    expect(isRedirect(error)).toBe(false)
  })
})
