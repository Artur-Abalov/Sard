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
})
