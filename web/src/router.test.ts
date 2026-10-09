// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createMemoryHistory, isRedirect } from '@tanstack/react-router'
import { QueryClient } from '@tanstack/react-query'
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { guard } from './auth/guard'
import { http } from './mocks/http'
import { noSession, PROBLEM } from './mocks/problems'
import { server } from './mocks/node'
import { NotFound } from './pages/NotFound'
import { createAppRouter } from './router'

vi.mock('./auth/guard', { spy: true })

// Loads a path without rendering: matching, beforeLoad and loaders run, components do not.
async function load(path: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const router = createAppRouter({
    queryClient,
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  await router.load()
  return router
}

const sections = [
  { path: '/', routeId: '/_app/' },
  { path: '/agents', routeId: '/_app/agents/' },
  { path: '/agents/7', routeId: '/_app/agents/$agentId' },
  { path: '/tokens', routeId: '/_app/tokens' },
  { path: '/sources', routeId: '/_app/sources/' },
  { path: '/sources/new', routeId: '/_app/sources/new' },
  { path: '/sources/7', routeId: '/_app/sources/$sourceId/' },
  { path: '/sources/7/edit', routeId: '/_app/sources/$sourceId/edit' },
  { path: '/runs', routeId: '/_app/runs/' },
  { path: '/runs/42', routeId: '/_app/runs/$runId' },
  { path: '/settings', routeId: '/_app/settings' },
] as const

describe('routing', () => {
  beforeEach(() => {
    vi.mocked(guard).mockClear()
    server.use(
      http.get('/api/v1/session', ({ response }) =>
        response(200).json({
          tenantId: '00000000-0000-0000-0000-000000000001',
          expiresAt: new Date().toISOString(),
        }),
      ),
    )
  })

  test.each(sections)('$path opens its section inside the protected layout', async (section) => {
    const router = await load(section.path)
    const ids = router.state.matches.map((match) => match.routeId)
    expect(ids).toEqual(['__root__', '/_app', section.routeId])
    expect(router.state.matches.every((match) => match.status === 'success')).toBe(true)
  })

  test.each(sections)('$path passes the page guard before it opens', async (section) => {
    await load(section.path)
    expect(guard).toHaveBeenCalledOnce()
    expect(vi.mocked(guard).mock.calls[0][0].pathname).toBe(section.path)
  })

  test('a run card receives its id from the path', async () => {
    const router = await load('/runs/42')
    expect(router.state.matches.at(-1)?.params).toEqual({ runId: '42' })
  })

  test('the run filter is read from the address', async () => {
    const source = '0192f7a0-0000-7000-8000-000000000201'
    const router = await load(`/runs?status=failed&status=running&sourceId=${source}`)
    expect(router.state.matches.at(-1)?.search).toEqual({
      status: ['failed', 'running'],
      sourceId: source,
    })
    expect(router.state.location.searchStr).toBe(`?status=failed&status=running&sourceId=${source}`)
  })

  test('what is not valid in the run filter is dropped', async () => {
    const router = await load('/runs')
    const validate = router.routesById['/_app/runs/'].options.validateSearch as (
      search: Record<string, unknown>,
    ) => unknown
    expect(validate({ status: ['failed', 'done'], sourceId: 'not-a-uuid' })).toEqual({
      status: ['failed'],
    })
    expect(validate({ status: 'done' })).toEqual({})
  })

  test('a single status in the address is a filter of one', async () => {
    const router = await load('/runs?status=failed')
    expect(router.state.matches.at(-1)?.search).toEqual({ status: ['failed'] })
  })

  test('the pages that a checklist leads to take their hints from the address', async () => {
    expect((await load('/tokens?create=true&hint=enroll')).state.matches.at(-1)?.search).toEqual({
      create: true,
      hint: 'enroll',
    })
    expect((await load('/agents?hint=repo-init')).state.matches.at(-1)?.search).toEqual({
      hint: 'repo-init',
    })
    expect((await load('/sources?hint=run-backup')).state.matches.at(-1)?.search).toEqual({
      hint: 'run-backup',
    })
    const router = await load('/agents')
    const validate = router.routesById['/_app/agents/'].options.validateSearch as (
      search: Record<string, unknown>,
    ) => unknown
    expect(validate({ hint: 'other' })).toEqual({})
  })

  test('a new source can start with an agent', async () => {
    const router = await load('/sources/new?agentId=0192f7a0-0000-7000-8000-000000000101')
    expect(router.state.matches.at(-1)?.search).toEqual({
      agentId: '0192f7a0-0000-7000-8000-000000000101',
    })
  })

  test('an unknown path matches only the root, which renders the 404 page', async () => {
    const router = await load('/no-such-page')
    expect(router.state.matches.map((match) => match.routeId)).toEqual(['__root__'])
    expect(router.routesById.__root__.options.notFoundComponent).toBe(NotFound)
    expect(guard).not.toHaveBeenCalled()
  })

  describe('the wizard and sign-in around the administrator step (Рк1, Рк2)', () => {
    const adminStep = (state: 'pending' | 'done') =>
      server.use(
        http.get('/api/v1/session', ({ response }) => response(401).json(noSession, PROBLEM)),
        http.get('/api/v1/onboarding', ({ response }) =>
          response(200).json({
            access: 'none',
            setupCode: 'active',
            ca: null,
            caReplaceable: null,
            steps: [
              { id: 'ca', state },
              { id: 'admin', state },
              { id: 'self_backup', state: 'upcoming' },
              { id: 'keys_confirmed', state: 'upcoming' },
            ],
          }),
        ),
      )

    // What the route's beforeLoad throws for a visit: the redirect options, or null.
    async function redirectOf(path: string, routeId: '/setup' | '/login' | '/_app') {
      const router = await load('/no-such-page')
      const beforeLoad = router.routesById[routeId].options.beforeLoad as (args: unknown) => unknown
      const location = router.parseLocation(
        createMemoryHistory({ initialEntries: [path] }).location,
      )
      try {
        await beforeLoad({ location, search: location.search })
        return null
      } catch (thrown) {
        return isRedirect(thrown) ? thrown.options : null
      }
    }

    test('a protected page before the admin step redirects to /setup without a parameter', async () => {
      vi.mocked(guard).mockRestore()
      adminStep('pending')
      expect(await redirectOf('/agents', '/_app')).toMatchObject({ to: '/setup' })
    })

    test('/settings without a session redirects to /login with its path', async () => {
      vi.mocked(guard).mockRestore()
      adminStep('done')
      expect(await redirectOf('/settings', '/_app')).toMatchObject({
        to: '/login',
        search: { redirect: '/settings' },
      })
    })

    test('/setup opens before the admin step', async () => {
      adminStep('pending')
      const router = await load('/setup')
      expect(router.state.location.pathname).toBe('/setup')
      expect(router.state.matches.map((match) => match.routeId)).toEqual(['__root__', '/setup'])
      expect(guard).not.toHaveBeenCalled()
    })

    test('/setup after the admin step redirects to /login', async () => {
      adminStep('done')
      expect(await redirectOf('/setup', '/setup')).toMatchObject({ to: '/login' })
    })

    test('/login before the admin step redirects to /setup', async () => {
      adminStep('pending')
      expect(await redirectOf('/login?redirect=%2Fruns', '/login')).toMatchObject({ to: '/setup' })
    })
  })
})
