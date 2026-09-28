// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import createClient from 'openapi-fetch'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import type { paths } from '../api/schema'
import { ids, MOCK_PASSWORD } from './fixtures'

// The mock API as W2 pages see it: the typed client against the MSW handlers.
// vitest.setup.ts resets the mock state (signed out) after every test.
const api = createClient<paths>({ baseUrl: location.origin })

const unknownId = '0192f7a0-0000-7000-8000-00000000ffff'

/** The body of a successful response; fails the test otherwise. */
function must<T>(result: { data?: T; response: Response }): T {
  if (result.data === undefined) throw new Error(`${result.response.status} ${result.response.url}`)
  return result.data
}

async function signIn() {
  const { response } = await api.POST('/api/v1/session', { body: { password: MOCK_PASSWORD } })
  expect(response.status).toBe(204)
}

describe('session', () => {
  test('sign in, check and sign out', async () => {
    await signIn()
    const { data } = await api.GET('/api/v1/session')
    expect(data?.tenantId).toBeTruthy()
    expect((await api.DELETE('/api/v1/session')).response.status).toBe(204)
    expect((await api.GET('/api/v1/session')).response.status).toBe(401)
  })

  test('a wrong password is 401, and repeated failures lock sign-in with Retry-After', async () => {
    const wrong = await api.POST('/api/v1/session', { body: { password: 'nope' } })
    expect(wrong.response.status).toBe(401)
    expect(wrong.error?.code).toBe('unauthenticated')
    let last = wrong
    for (let i = 0; i < 5; i++)
      last = await api.POST('/api/v1/session', { body: { password: 'nope' } })
    expect(last.response.status).toBe(429)
    expect(last.response.headers.get('Retry-After')).toBe('900')
    const right = await api.POST('/api/v1/session', { body: { password: MOCK_PASSWORD } })
    expect(right.response.status).toBe(429)
  })

  describe('the lock window (К4: matches the server)', () => {
    beforeEach(() => vi.useFakeTimers())
    afterEach(() => vi.useRealTimers())

    test('15 minutes after the fifth failure sign-in works again', async () => {
      for (let i = 0; i < 5; i++) await api.POST('/api/v1/session', { body: { password: 'nope' } })
      vi.advanceTimersByTime(15 * 60_000)
      const response = await api.POST('/api/v1/session', { body: { password: MOCK_PASSWORD } })
      expect(response.response.status).toBe(204)
    })

    test('failures older than 15 minutes drop out of the window', async () => {
      for (let i = 0; i < 4; i++) await api.POST('/api/v1/session', { body: { password: 'nope' } })
      vi.advanceTimersByTime(15 * 60_000)
      for (let i = 0; i < 4; i++) await api.POST('/api/v1/session', { body: { password: 'nope' } })
      const response = await api.POST('/api/v1/session', { body: { password: 'nope' } })
      expect(response.response.status).toBe(401)
      expect(response.error?.code).toBe('unauthenticated')
    })

    test('a success clears the failure counter', async () => {
      for (let i = 0; i < 4; i++) await api.POST('/api/v1/session', { body: { password: 'nope' } })
      await signIn()
      for (let i = 0; i < 4; i++) await api.POST('/api/v1/session', { body: { password: 'nope' } })
      const response = await api.POST('/api/v1/session', { body: { password: 'nope' } })
      expect(response.response.status).toBe(401)
    })
  })

  test('the session cookie has Path=/, and the session names the default tenant with a 12-hour deadline', async () => {
    const signedIn = await api.POST('/api/v1/session', { body: { password: MOCK_PASSWORD } })
    expect(signedIn.response.headers.get('Set-Cookie')).toContain('Path=/')
    const before = Date.now()
    const { data } = await api.GET('/api/v1/session')
    expect(data?.tenantId).toBe('0192f7a0-0000-7000-8000-00000000000a')
    const ttl = Date.parse(data?.expiresAt ?? '') - before
    expect(Math.abs(ttl - 12 * 3_600_000)).toBeLessThan(60_000)
  })

  test('a mutating request with a foreign Origin is 403 origin_rejected; the session survives', async () => {
    await signIn()
    const response = await fetch(new URL('/api/v1/session', location.origin), {
      method: 'DELETE',
      headers: { Origin: 'https://evil.example' },
    })
    expect(response.status).toBe(403)
    expect((await response.json()).code).toBe('origin_rejected')
    expect((await api.GET('/api/v1/session')).response.status).toBe(200)
  })

  test('without a session every protected endpoint answers 401 problem+json', async () => {
    const source = {
      name: 'n',
      agentId: ids.dbAgent,
      plugin: 'files',
      repositoryName: 'local',
      config: {},
    }
    const path = { params: { path: { sourceId: ids.etcSource } } }
    const calls = [
      api.GET('/api/v1/session'),
      api.DELETE('/api/v1/session'),
      api.GET('/api/v1/agents'),
      api.GET('/api/v1/agents/{agentId}', { params: { path: { agentId: ids.dbAgent } } }),
      api.POST('/api/v1/enrollment-tokens', { body: {} }),
      api.GET('/api/v1/enrollment-tokens'),
      api.GET('/api/v1/enrollment-tokens/{tokenId}', {
        params: { path: { tokenId: ids.activeToken } },
      }),
      api.POST('/api/v1/enrollment-tokens/{tokenId}/revoke', {
        params: { path: { tokenId: ids.activeToken } },
      }),
      api.GET('/api/v1/sources'),
      api.POST('/api/v1/sources', { body: source }),
      api.GET('/api/v1/sources/{sourceId}', path),
      api.PUT('/api/v1/sources/{sourceId}', { ...path, body: source }),
      api.DELETE('/api/v1/sources/{sourceId}', path),
      api.POST('/api/v1/sources/{sourceId}/runs', path),
      api.GET('/api/v1/sources/{sourceId}/snapshots', path),
      api.GET('/api/v1/runs'),
      api.GET('/api/v1/runs/{runId}', { params: { path: { runId: ids.runningRun } } }),
      api.GET('/api/v1/runs/{runId}/steps/{stepId}/logs', {
        params: { path: { runId: ids.runningRun, stepId: ids.runningStep } },
      }),
    ]
    for (const { response, error } of await Promise.all(calls)) {
      expect(response.status, response.url).toBe(401)
      expect(response.headers.get('Content-Type')).toBe('application/problem+json')
      expect(error).toMatchObject({ code: 'unauthenticated' })
    }
  })

  test('status stays public', async () => {
    expect((await api.GET('/api/v1/status')).response.status).toBe(200)
  })
})

describe('with a session', () => {
  beforeEach(signIn)

  test('agents: one online, one offline, the card carries plugins, repositories and names', async () => {
    const { data } = await api.GET('/api/v1/agents')
    expect(data?.items.map((a) => a.status).sort()).toEqual(['offline', 'online'])
    const online = await api.GET('/api/v1/agents', { params: { query: { status: 'online' } } })
    expect(online.data?.items.map((a) => a.id)).toEqual([ids.dbAgent])
    const card = await api.GET('/api/v1/agents/{agentId}', {
      params: { path: { agentId: ids.dbAgent } },
    })
    expect(card.data?.plugins[0].configSchema).toMatchObject({ type: 'object' })
    expect(card.data?.secretNames).toEqual(['pg-password'])
    const missing = await api.GET('/api/v1/agents/{agentId}', {
      params: { path: { agentId: unknownId } },
    })
    expect(missing.response.status).toBe(404)
  })

  test('the log of a step pages through every line once, in order', async () => {
    const seqs: number[] = []
    let afterSeq = 0
    for (let pages = 0; pages < 100; pages++) {
      const { data } = await api.GET('/api/v1/runs/{runId}/steps/{stepId}/logs', {
        params: {
          path: { runId: ids.runningRun, stepId: ids.runningStep },
          query: { afterSeq, limit: 100 },
        },
      })
      if (data === undefined) throw new Error('no page')
      seqs.push(...data.items.map((line) => line.seq))
      afterSeq = data.nextAfterSeq
      if (!data.hasMore) break
    }
    expect(seqs.length).toBeGreaterThanOrEqual(300)
    expect(seqs).toEqual(Array.from({ length: seqs.length }, (_, i) => i + 1))
  })

  test('a step of another run is not found', async () => {
    const { response } = await api.GET('/api/v1/runs/{runId}/steps/{stepId}/logs', {
      params: { path: { runId: ids.failedRun, stepId: ids.runningStep } },
    })
    expect(response.status).toBe(404)
  })

  test('runs: succeeded, failed and running; filters and pages', async () => {
    const all = must(await api.GET('/api/v1/runs')).items
    expect(all.map((r) => r.status)).toEqual(['running', 'failed', 'succeeded'])
    const finished = must(
      await api.GET('/api/v1/runs', { params: { query: { status: ['succeeded', 'failed'] } } }),
    )
    expect(finished.items.map((r) => r.id)).toEqual([ids.failedRun, ids.succeededRun])
    const first = must(await api.GET('/api/v1/runs', { params: { query: { limit: 2 } } }))
    const cursor = first.nextCursor ?? 'missing'
    const rest = must(await api.GET('/api/v1/runs', { params: { query: { limit: 2, cursor } } }))
    expect([...first.items, ...rest.items].map((r) => r.id)).toEqual(all.map((r) => r.id))
    expect(rest.nextCursor).toBeNull()
    const run = must(
      await api.GET('/api/v1/runs/{runId}', { params: { path: { runId: ids.failedRun } } }),
    )
    expect(run.steps[0].message).toContain('permission denied')
  })

  test('a second run of a source with an active run is 409 with the active run', async () => {
    const path = { params: { path: { sourceId: ids.etcSource } } }
    const { response, error } = await api.POST('/api/v1/sources/{sourceId}/runs', path)
    expect(response.status).toBe(409)
    expect(error).toMatchObject({ code: 'run_active', activeRunId: ids.runningRun })
  })

  test('a run of an idle source is queued, and then it is the active one', async () => {
    const path = { params: { path: { sourceId: ids.homeSource } } }
    const created = await api.POST('/api/v1/sources/{sourceId}/runs', path)
    expect(created.response.status).toBe(201)
    expect(created.data).toMatchObject({
      sourceId: ids.homeSource,
      status: 'queued',
      trigger: 'manual',
    })
    expect(created.data?.steps).toHaveLength(1)
    const again = await api.POST('/api/v1/sources/{sourceId}/runs', path)
    expect(again.error).toMatchObject({ code: 'run_active', activeRunId: created.data?.id })
    const listed = await api.GET('/api/v1/runs', {
      params: { query: { sourceId: ids.homeSource } },
    })
    expect(listed.data?.items.map((r) => r.id)).toEqual([created.data?.id])
  })

  test('snapshots of a source', async () => {
    const path = { params: { path: { sourceId: ids.etcSource } } }
    const { data } = await api.GET('/api/v1/sources/{sourceId}/snapshots', path)
    expect(data?.items.map((s) => s.snapshotId)).toEqual(['4f1c2a9e'])
  })
})

describe('sources', () => {
  beforeEach(signIn)
  const input = {
    name: 'db1 /var',
    agentId: ids.dbAgent,
    plugin: 'files',
    repositoryName: 'local',
    config: {},
  }

  test('create, read, replace and delete', async () => {
    const created = await api.POST('/api/v1/sources', { body: input })
    expect(created.response.status).toBe(201)
    const sourceId = created.data?.id ?? ''
    const path = { params: { path: { sourceId } } }
    expect((await api.GET('/api/v1/sources/{sourceId}', path)).data?.name).toBe('db1 /var')
    const replaced = await api.PUT('/api/v1/sources/{sourceId}', {
      ...path,
      body: { ...input, name: 'var' },
    })
    expect(replaced.data?.name).toBe('var')
    expect(
      (await api.GET('/api/v1/sources', { params: { query: { agentId: ids.dbAgent } } })).data
        ?.items,
    ).toHaveLength(2)
    expect((await api.DELETE('/api/v1/sources/{sourceId}', path)).response.status).toBe(204)
    expect((await api.GET('/api/v1/sources/{sourceId}', path)).response.status).toBe(404)
  })

  test('a repository the agent did not report is 422 unknown_repository', async () => {
    const { response, error } = await api.POST('/api/v1/sources', {
      body: { ...input, repositoryName: 's3' },
    })
    expect(response.status).toBe(422)
    expect(response.headers.get('Content-Type')).toBe('application/problem+json')
    expect(error).toMatchObject({
      code: 'unknown_repository',
      errors: [{ field: 'repositoryName' }],
    })
  })

  test('replacing with an unknown repository is 422 too', async () => {
    const path = { params: { path: { sourceId: ids.homeSource } } }
    const { error } = await api.PUT('/api/v1/sources/{sourceId}', {
      ...path,
      body: { ...input, repositoryName: 's3' },
    })
    expect(error?.code).toBe('unknown_repository')
  })

  test('a source with an active run cannot be deleted', async () => {
    const { response, error } = await api.DELETE('/api/v1/sources/{sourceId}', {
      params: { path: { sourceId: ids.etcSource } },
    })
    expect(response.status).toBe(409)
    expect(error).toMatchObject({ code: 'run_active', activeRunId: ids.runningRun })
  })
})

describe('enrollment tokens', () => {
  beforeEach(signIn)
  const tokenPath = (tokenId: string) => ({ params: { path: { tokenId } } })

  test('the list has all four states and never the token string', async () => {
    const created = must(await api.POST('/api/v1/enrollment-tokens', { body: {} }))
    const list = must(await api.GET('/api/v1/enrollment-tokens'))
    expect(new Set(list.items.map((t) => t.status))).toEqual(
      new Set(['active', 'used', 'expired', 'revoked']),
    )
    expect(JSON.stringify(list)).not.toContain(created.token)
    for (const item of list.items) {
      expect(Object.keys(item)).not.toContain('token')
      expect(Object.keys(item)).not.toContain('enrollCommand')
    }
    const card = must(await api.GET('/api/v1/enrollment-tokens/{tokenId}', tokenPath(created.id)))
    expect(JSON.stringify(card)).not.toContain(created.token)
    const used = must(
      await api.GET('/api/v1/enrollment-tokens', { params: { query: { status: 'used' } } }),
    )
    expect(used.items.map((t) => t.agentId)).toEqual([ids.dbAgent])
  })

  test('creation returns the token and a ready enroll command with it; 24 hours by default', async () => {
    const before = Date.now()
    const { data } = await api.POST('/api/v1/enrollment-tokens', { body: {} })
    expect(data?.token).toMatch(/^sard_[A-Za-z0-9_-]{43}\.[0-9a-f]{64}$/)
    expect(data?.enrollCommand).toBe(
      `sard-agent enroll --server sard.example.com:9090 --token ${data?.token}`,
    )
    const ttl = Date.parse(data?.expiresAt ?? '') - before
    expect(Math.abs(ttl - 86_400_000)).toBeLessThan(60_000)
  })

  test('a lifetime outside 5 minutes to 7 days is 422 naming the field, and no token is made', async () => {
    const { response, error } = await api.POST('/api/v1/enrollment-tokens', {
      body: { ttlSeconds: 604_801 },
    })
    expect(response.status).toBe(422)
    expect(error).toMatchObject({ errors: [{ field: 'ttlSeconds' }] })
    expect((await api.GET('/api/v1/enrollment-tokens')).data?.items).toHaveLength(4)
  })

  test('revoking an active token makes it revoked; revoking again is still 200', async () => {
    const first = await api.POST(
      '/api/v1/enrollment-tokens/{tokenId}/revoke',
      tokenPath(ids.activeToken),
    )
    expect(first.data).toMatchObject({ status: 'revoked' })
    expect(first.data?.revokedAt).toBeTruthy()
    const again = await api.POST(
      '/api/v1/enrollment-tokens/{tokenId}/revoke',
      tokenPath(ids.activeToken),
    )
    expect(again.response.status).toBe(200)
    expect(again.data?.revokedAt).toBe(first.data?.revokedAt)
  })

  test('a used token cannot be revoked: 409 names the agent; an expired one: 409', async () => {
    const used = await api.POST(
      '/api/v1/enrollment-tokens/{tokenId}/revoke',
      tokenPath(ids.usedToken),
    )
    expect(used.response.status).toBe(409)
    expect(used.error).toMatchObject({ code: 'token_used', agentId: ids.dbAgent })
    const expired = await api.POST(
      '/api/v1/enrollment-tokens/{tokenId}/revoke',
      tokenPath(ids.expiredToken),
    )
    expect(expired.response.status).toBe(409)
    expect(expired.error).toMatchObject({ code: 'token_expired', agentId: null })
  })

  test('an unknown token is 404', async () => {
    const { response } = await api.GET('/api/v1/enrollment-tokens/{tokenId}', tokenPath(unknownId))
    expect(response.status).toBe(404)
  })
})
