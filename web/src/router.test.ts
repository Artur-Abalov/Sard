// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createMemoryHistory } from '@tanstack/react-router'
import { QueryClient } from '@tanstack/react-query'
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { guard } from './auth/guard'
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
  { path: '/agents', routeId: '/_app/agents' },
  { path: '/sources', routeId: '/_app/sources' },
  { path: '/runs', routeId: '/_app/runs/' },
  { path: '/runs/42', routeId: '/_app/runs/$runId' },
] as const

describe('routing', () => {
  beforeEach(() => {
    vi.mocked(guard).mockClear()
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

  test('an unknown path matches only the root, which renders the 404 page', async () => {
    const router = await load('/no-such-page')
    expect(router.state.matches.map((match) => match.routeId)).toEqual(['__root__'])
    expect(router.routesById.__root__.options.notFoundComponent).toBe(NotFound)
    expect(guard).not.toHaveBeenCalled()
  })
})
