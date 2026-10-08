// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import createClient from 'openapi-fetch'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import type { paths } from '../api/schema'
import filesSchema from '../../../agent/plugins/files/schema.json'
import { agents, ids, MOCK_PASSWORD, runs, snapshots, stepLogs } from './fixtures'
import { state } from './state'

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

    test('Retry-After counts down from the fifth failure, like the server', async () => {
      for (let i = 0; i < 5; i++) await api.POST('/api/v1/session', { body: { password: 'nope' } })
      vi.advanceTimersByTime(10 * 60_000)
      const response = await api.POST('/api/v1/session', { body: { password: 'nope' } })
      expect(response.response.status).toBe(429)
      expect(response.response.headers.get('Retry-After')).toBe('300')
    })

    test('Retry-After rounds up to a whole second, like the server', async () => {
      for (let i = 0; i < 5; i++) await api.POST('/api/v1/session', { body: { password: 'nope' } })
      vi.advanceTimersByTime(14 * 60_000 + 59_000 + 999)
      const response = await api.POST('/api/v1/session', { body: { password: 'nope' } })
      expect(response.response.headers.get('Retry-After')).toBe('1')
    })

    test('attempts during the lock do not extend it', async () => {
      for (let i = 0; i < 5; i++) await api.POST('/api/v1/session', { body: { password: 'nope' } })
      vi.advanceTimersByTime(60_000)
      await api.POST('/api/v1/session', { body: { password: 'nope' } })
      vi.advanceTimersByTime(14 * 60_000)
      const response = await api.POST('/api/v1/session', { body: { password: MOCK_PASSWORD } })
      expect(response.response.status).toBe(204)
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
      api.POST('/api/v1/agents/{agentId}/revoke', { params: { path: { agentId: ids.dbAgent } } }),
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
    expect(data?.items.filter((a) => a.status === 'online').map((a) => a.hostname)).toEqual([
      'db1.example.com',
      'sard-self',
    ])
    const online = await api.GET('/api/v1/agents', { params: { query: { status: 'online' } } })
    expect(online.data?.items.map((a) => a.id).sort()).toEqual([ids.dbAgent, ids.selfAgent].sort())
    const card = await api.GET('/api/v1/agents/{agentId}', {
      params: { path: { agentId: ids.dbAgent } },
    })
    expect(card.data?.plugins[0].configSchema).toMatchObject({ type: 'object' })
    expect(card.data?.secretNames).toEqual(['pg-password', 'api-token'])
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

  test('runs: every status, filters and pages', async () => {
    const all = must(await api.GET('/api/v1/runs')).items
    expect(all.map((r) => r.status)).toEqual([
      'queued',
      'running',
      'failed',
      'failed',
      'failed',
      'failed',
      'failed',
      'succeeded',
      'succeeded',
    ])
    const finished = must(
      await api.GET('/api/v1/runs', { params: { query: { status: ['succeeded', 'failed'] } } }),
    )
    expect(finished.items.map((r) => r.id)).toEqual(
      all.filter((r) => r.status !== 'queued' && r.status !== 'running').map((r) => r.id),
    )
    const first = must(await api.GET('/api/v1/runs', { params: { query: { limit: 20 } } }))
    expect(first.nextCursor).toBeNull()
    const head = must(await api.GET('/api/v1/runs', { params: { query: { limit: 2 } } }))
    const cursor = head.nextCursor ?? 'missing'
    const next = must(await api.GET('/api/v1/runs', { params: { query: { limit: 2, cursor } } }))
    expect([...head.items, ...next.items].map((r) => r.id)).toEqual(
      all.slice(0, 4).map((r) => r.id),
    )
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
    expect(data?.items.map((s) => s.snapshotId)).toEqual(['a1b2c3', '4f1c2a9e'])
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

describe('agent install', () => {
  beforeEach(signIn)

  test('the details answer with the arch and format asked for, the steps of the block', async () => {
    const { data } = await api.GET('/api/v1/agent-install', {
      params: { query: { arch: 'arm64', format: 'tar' } },
    })
    expect(data).toMatchObject({ arch: 'arm64', format: 'tar', downloadsEnabled: true })
    expect(data?.steps.map((step) => step.kind)).toEqual([
      'download',
      'checksum',
      'signature',
      'install',
      'configure',
      'enroll',
      'repo-init',
      'start',
    ])
    expect(data?.steps[0].commands[0]).toContain('arm64.tar.gz')
  })

  test('the details of rpm install the file with rpm -Uvh', async () => {
    const { data } = await api.GET('/api/v1/agent-install', {
      params: { query: { arch: 'arm64', format: 'rpm' } },
    })
    expect(data).toMatchObject({ arch: 'arm64', format: 'rpm' })
    expect(data?.steps.find((step) => step.kind === 'install')?.commands).toEqual([
      'sudo rpm -Uvh sard-agent-v1.4.0.aarch64.rpm',
    ])
  })

  test('the upgrade of an agent in rpm is rpm -Uvh of its architecture', async () => {
    const agent = state.agents.find((a) => a.arch === 'amd64')
    expect(agent).toBeDefined()
    const { data } = await api.GET('/api/v1/agents/{agentId}/upgrade', {
      params: { path: { agentId: agent!.id }, query: { format: 'rpm' } },
    })
    expect(data?.format).toBe('rpm')
    expect(data?.steps.find((step) => step.kind === 'upgrade')?.commands).toEqual([
      'sudo rpm -Uvh sard-agent-v1.4.0.x86_64.rpm',
    ])
  })

  test('Мок отдаёт признак сохранения конфигурации по формату', async () => {
    const agent = state.agents.find((a) => a.arch === 'amd64')
    expect(agent).toBeDefined()
    const flags: (boolean | undefined)[] = []
    for (const format of ['deb', 'rpm', 'tar'] as const) {
      const { data } = await api.GET('/api/v1/agents/{agentId}/upgrade', {
        params: { path: { agentId: agent!.id }, query: { format } },
      })
      flags.push(data?.keepsConfiguration)
    }
    expect(flags).toEqual([true, true, false])
  })

  test('Мок без команд обновления отдаёт keepsConfiguration false', async () => {
    const { data } = await api.GET('/api/v1/agents/{agentId}/upgrade', {
      params: { path: { agentId: ids.bareAgent }, query: { format: 'deb' } },
    })
    expect(data).toMatchObject({ reason: 'arch_unknown', keepsConfiguration: false })
  })

  test('the defaults are deb and amd64, and the answer holds no token string', async () => {
    const { data } = await api.GET('/api/v1/agent-install')
    expect(data).toMatchObject({ arch: 'amd64', format: 'deb' })
    expect(JSON.stringify(data)).not.toContain('sard_')
  })

  test('an unknown architecture is 422 naming the field', async () => {
    const { response, error } = await api.GET('/api/v1/agent-install', {
      params: { query: { arch: 'riscv64' as 'amd64' } },
    })
    expect(response.status).toBe(422)
    expect(error).toMatchObject({ errors: [{ field: 'arch' }] })
  })

  test('without a session it is 401', async () => {
    await api.DELETE('/api/v1/session')
    expect((await api.GET('/api/v1/agent-install')).response.status).toBe(401)
  })

  test('the upgrade of an agent uses its architecture; an agent that never registered has a reason', async () => {
    const web = await api.GET('/api/v1/agents/{agentId}/upgrade', {
      params: { path: { agentId: ids.webAgent } },
    })
    expect(web.data?.arch).toBe('arm64')
    expect(web.data?.steps[0].commands[0]).toContain('arm64')
    const bare = await api.GET('/api/v1/agents/{agentId}/upgrade', {
      params: { path: { agentId: ids.bareAgent } },
    })
    expect(bare.data).toMatchObject({ steps: [], reason: 'arch_unknown' })
  })

  test('the upgrade of an unknown agent is 404', async () => {
    const { response } = await api.GET('/api/v1/agents/{agentId}/upgrade', {
      params: { path: { agentId: unknownId } },
    })
    expect(response.status).toBe(404)
  })

  test('the list marks the outdated agent and no other', async () => {
    const list = must(await api.GET('/api/v1/agents'))
    expect(list.items.filter((a) => a.outdated).map((a) => a.id)).toEqual([ids.webAgent])
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
      `sudo -u sard-agent sard-agent enroll --server sard.example.com:9090 --token ${data?.token}`,
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

describe('agent revocation, soft delete and the new fields (S8b)', () => {
  beforeEach(signIn)
  const agentPath = (agentId: string) => ({ params: { path: { agentId } } })

  test('revoking an agent keeps it in the list as revoked and offline; again changes nothing', async () => {
    const first = await api.POST('/api/v1/agents/{agentId}/revoke', agentPath(ids.dbAgent))
    expect(first.data).toMatchObject({ status: 'offline' })
    expect(first.data?.revokedAt).toBeTruthy()
    const again = await api.POST('/api/v1/agents/{agentId}/revoke', agentPath(ids.dbAgent))
    expect(again.data?.revokedAt).toBe(first.data?.revokedAt)
    const listed = must(await api.GET('/api/v1/agents')).items.find((a) => a.id === ids.dbAgent)
    expect(listed).toMatchObject({ status: 'offline', duplicateSessionAt: null })
    expect(listed?.revokedAt).toBe(first.data?.revokedAt)
  })

  const revokeSelf = (confirm?: string) =>
    api.POST('/api/v1/agents/{agentId}/revoke', {
      params: { path: { agentId: ids.selfAgent }, query: confirm === undefined ? {} : { confirm } },
    })

  test('the built-in agent is listed with builtin true, the others with false', async () => {
    const items = must(await api.GET('/api/v1/agents')).items
    expect(items.filter((a) => a.builtin).map((a) => a.id)).toEqual([ids.selfAgent])
    expect(items.length).toBeGreaterThan(1)
  })

  test('revoking the built-in agent without the exact confirmation is 409 and changes nothing', async () => {
    for (const confirm of [undefined, '', 'SARD-SELF', 'sard-self ', 'yes']) {
      const { response, error } = await revokeSelf(confirm)
      expect(response.status, String(confirm)).toBe(409)
      expect(error).toMatchObject({ code: 'self_agent_confirmation_required' })
    }
    const card = must(await api.GET('/api/v1/agents/{agentId}', agentPath(ids.selfAgent)))
    expect(card).toMatchObject({ revokedAt: null, status: 'online' })
  })

  test('with confirm sard-self the built-in agent is revoked; again without it is 200 and unchanged', async () => {
    const first = await revokeSelf('sard-self')
    expect(first.data).toMatchObject({ status: 'offline', builtin: true })
    expect(first.data?.revokedAt).toBeTruthy()
    const again = await revokeSelf()
    expect(again.response.status).toBe(200)
    expect(again.data?.revokedAt).toBe(first.data?.revokedAt)
  })

  test('an ordinary agent is revoked with or without confirm', async () => {
    const { response } = await api.POST('/api/v1/agents/{agentId}/revoke', {
      params: { path: { agentId: ids.dbAgent }, query: { confirm: 'yes' } },
    })
    expect(response.status).toBe(200)
  })

  test('an unknown agent is 404 on revoke', async () => {
    const { response } = await api.POST('/api/v1/agents/{agentId}/revoke', agentPath(unknownId))
    expect(response.status).toBe(404)
  })

  test('a revoked agent cannot be named by a source (422) and its source cannot start (409)', async () => {
    await api.POST('/api/v1/agents/{agentId}/revoke', agentPath(ids.dbAgent))
    const input = {
      name: 'x',
      agentId: ids.dbAgent,
      plugin: 'files',
      repositoryName: 'local',
      config: {},
    }
    const created = await api.POST('/api/v1/sources', { body: input })
    expect(created.response.status).toBe(422)
    expect(created.error).toMatchObject({ code: 'agent_revoked' })
    const run = await api.POST('/api/v1/sources/{sourceId}/runs', {
      params: { path: { sourceId: ids.homeSource } },
    })
    expect(run.response.status).toBe(201)
    await api.POST('/api/v1/agents/{agentId}/revoke', agentPath(ids.webAgent))
    const second = await api.POST('/api/v1/sources/{sourceId}/runs', {
      params: { path: { sourceId: ids.etcSource } },
    })
    expect(second.response.status).toBe(409)
  })

  test('a deleted source keeps its runs and snapshots; its card is 404', async () => {
    const path = { params: { path: { sourceId: ids.homeSource } } }
    expect((await api.DELETE('/api/v1/sources/{sourceId}', path)).response.status).toBe(204)
    expect((await api.GET('/api/v1/sources/{sourceId}', path)).response.status).toBe(404)
    expect((await api.GET('/api/v1/sources/{sourceId}/snapshots', path)).response.status).toBe(200)
    const unknown = { params: { path: { sourceId: unknownId } } }
    expect((await api.GET('/api/v1/sources/{sourceId}/snapshots', unknown)).response.status).toBe(
      404,
    )
  })

  test('a started run names its source', async () => {
    const run = must(
      await api.POST('/api/v1/sources/{sourceId}/runs', {
        params: { path: { sourceId: ids.homeSource } },
      }),
    )
    expect(run).toMatchObject({ sourceName: 'web2 /home', sourceDeleted: false })
  })

  test('a started run has its first step at ordinal 0', async () => {
    const run = must(
      await api.POST('/api/v1/sources/{sourceId}/runs', {
        params: { path: { sourceId: ids.homeSource } },
      }),
    )
    expect(run.steps[0].ordinal).toBe(0)
  })

  test('a token label is kept, empty means none, more than 200 characters is 422', async () => {
    const labelled = must(await api.POST('/api/v1/enrollment-tokens', { body: { label: 'db1' } }))
    const card = must(
      await api.GET('/api/v1/enrollment-tokens/{tokenId}', {
        params: { path: { tokenId: labelled.id } },
      }),
    )
    expect(card.label).toBe('db1')
    expect(labelled.agentEndpointConfigured).toBe(false)
    const empty = must(await api.POST('/api/v1/enrollment-tokens', { body: { label: '' } }))
    const emptyCard = must(
      await api.GET('/api/v1/enrollment-tokens/{tokenId}', {
        params: { path: { tokenId: empty.id } },
      }),
    )
    expect(emptyCard.label).toBeNull()
    const long = await api.POST('/api/v1/enrollment-tokens', { body: { label: 'x'.repeat(201) } })
    expect(long.response.status).toBe(422)
    expect(long.error).toMatchObject({ errors: [{ field: 'label' }] })
  })

  test('runs are filtered by the time they were queued: from included, to excluded', async () => {
    const queued = (await api.GET('/api/v1/runs')).data?.items.map((r) => r.queuedAt) ?? []
    const [newest, middle] = queued
    const found = must(
      await api.GET('/api/v1/runs', {
        params: { query: { queuedFrom: middle, queuedTo: newest } },
      }),
    )
    expect(found.items.map((r) => r.queuedAt)).toEqual([middle])
  })
})

describe('W2 mocks', () => {
  beforeEach(signIn)
  const agentPath = (agentId: string) => ({ params: { path: { agentId } } })
  const overview = async () => must(await api.GET('/api/v1/overview'))
  const marks = (steps: Awaited<ReturnType<typeof overview>>['firstSteps']) =>
    Object.entries(steps)
      .filter(([, done]) => !done)
      .map(([step]) => step)

  test('the files schema of the mocks is the agent’s', () => {
    for (const agent of agents) {
      for (const plugin of agent.plugins.filter((p) => p.name === 'files')) {
        expect(plugin.configSchema).toEqual(filesSchema)
      }
    }
    expect(agents.some((a) => a.plugins.some((p) => p.name === 'files'))).toBe(true)
  })

  test('the fixtures cover the states of W2', () => {
    expect(agents.some((a) => a.revokedAt !== null)).toBe(true)
    expect(agents.some((a) => a.duplicateSessionAt !== null)).toBe(true)
    expect(agents.some((a) => a.protocolVersion === null && a.plugins.length === 0)).toBe(true)
    expect(agents.some((a) => a.repositories.some((r) => r.repositoryId === null))).toBe(true)
    const steps = runs.flatMap((r) => r.steps)
    for (const status of ['lost', 'rejected', 'timed_out'] as const) {
      expect(
        steps.some((s) => s.status === status),
        status,
      ).toBe(true)
    }
    const backups = steps.flatMap((s) => (s.backup === null ? [] : [{ step: s, backup: s.backup }]))
    expect(backups.some((b) => b.step.status === 'failed' && b.backup.partial)).toBe(true)
    expect(backups.some((b) => b.step.status === 'lost' && !b.backup.partial)).toBe(true)
    expect(snapshots.some((s) => s.partial)).toBe(true)
    expect(runs.every((r) => r.sourceName !== '' && typeof r.sourceDeleted === 'boolean')).toBe(
      true,
    )
    const lines = Object.values(stepLogs)
    expect(lines.some((l) => l.at(-1)?.time === null)).toBe(true)
    expect(lines.some((l) => l.some((line) => line.text.includes('[REDACTED]')))).toBe(true)
    const offline = agents.filter((a) => a.status === 'offline').map((a) => a.id)
    const queued = runs.filter((r) => r.status === 'queued')
    expect(queued.some((r) => offline.includes(r.agentId))).toBe(true)
  })

  test('revoking an agent turns its active steps into lost and fails their runs', async () => {
    await api.POST('/api/v1/agents/{agentId}/revoke', agentPath(ids.dbAgent))

    const run = must(
      await api.GET('/api/v1/runs/{runId}', { params: { path: { runId: ids.runningRun } } }),
    )
    expect(run).toMatchObject({ status: 'failed', message: 'agent revoked' })
    expect(run.finishedAt).toBeTruthy()
    expect(run.steps[0]).toMatchObject({ status: 'lost', message: 'agent revoked' })
    expect(run.steps[0].finishedAt).toBeTruthy()
  })

  test('the overview counts the agents that are not revoked', async () => {
    const { agentsOnline, agentsTotal } = await overview()
    const live = agents.filter((a) => a.revokedAt === null)
    expect({ agentsOnline, agentsTotal }).toEqual({
      agentsOnline: live.filter((a) => a.status === 'online').length,
      agentsTotal: live.length,
    })
    expect(agentsTotal).toBeLessThan(agents.length)
    await api.POST('/api/v1/agents/{agentId}/revoke', agentPath(ids.dbAgent))
    expect(await overview()).toMatchObject({ agentsOnline: 1, agentsTotal: agentsTotal - 1 })
  })

  test('the overview marks the first steps by the rules of the server', async () => {
    const all = (await overview()).firstSteps
    expect(all).toEqual({
      tokenIssued: true,
      agentConnected: true,
      repositoryInitialized: true,
      sourceCreated: true,
      backupSucceeded: true,
      complete: true,
    })

    state.deletedSources.push(...state.sources.splice(0))
    expect(marks((await overview()).firstSteps)).toEqual(['sourceCreated', 'complete'])

    for (const agent of state.agents) agent.revokedAt = '2026-09-27T09:00:00Z'
    expect(marks((await overview()).firstSteps)).toEqual([
      'agentConnected',
      'repositoryInitialized',
      'sourceCreated',
      'complete',
    ])

    state.tokens = state.tokens.filter((t) => t.status === 'revoked')
    state.agents = []
    state.sources = []
    state.runs = []
    expect(marks((await overview()).firstSteps)).toEqual([
      'agentConnected',
      'repositoryInitialized',
      'sourceCreated',
      'backupSucceeded',
      'complete',
    ])

    state.tokens = []
    expect((await overview()).firstSteps).toEqual({
      tokenIssued: false,
      agentConnected: false,
      repositoryInitialized: false,
      sourceCreated: false,
      backupSucceeded: false,
      complete: false,
    })
  })

  test('a deleted source keeps its name in its runs, which are marked deleted', async () => {
    const run = state.runs.find((r) => r.id === ids.runningRun)
    if (run === undefined) throw new Error('no fixture run')
    Object.assign(run, { status: 'succeeded' })
    run.steps[0].status = 'succeeded'
    await api.DELETE('/api/v1/sources/{sourceId}', {
      params: { path: { sourceId: ids.etcSource } },
    })

    const listed = must(await api.GET('/api/v1/runs')).items.find((r) => r.id === ids.runningRun)
    const card = must(
      await api.GET('/api/v1/runs/{runId}', { params: { path: { runId: ids.runningRun } } }),
    )

    for (const item of [listed, card]) {
      expect(item).toMatchObject({ sourceName: 'db1 /etc', sourceDeleted: true })
    }
  })

  test('a renamed source is named by its new name in its runs', async () => {
    const source = must(
      await api.GET('/api/v1/sources/{sourceId}', {
        params: { path: { sourceId: ids.etcSource } },
      }),
    )
    await api.PUT('/api/v1/sources/{sourceId}', {
      params: { path: { sourceId: ids.etcSource } },
      body: { ...source, name: 'etc-main' },
    })

    const card = must(
      await api.GET('/api/v1/runs/{runId}', { params: { path: { runId: ids.failedRun } } }),
    )

    expect(card).toMatchObject({ sourceName: 'etc-main', sourceDeleted: false })
  })

  test('the backup output of a failed step is partial like its snapshot', async () => {
    const run = must(
      await api.GET('/api/v1/runs/{runId}', { params: { path: { runId: ids.partialRun } } }),
    )
    const step = run.steps[0]
    const snapshot = must(
      await api.GET('/api/v1/sources/{sourceId}/snapshots', {
        params: { path: { sourceId: run.sourceId } },
      }),
    ).items.find((s) => s.stepId === step.id)

    expect(step.status).toBe('failed')
    expect(step.backup?.partial).toBe(true)
    expect(snapshot?.partial).toBe(true)
  })

  test('the log that the server cut says so', async () => {
    const { data } = await api.GET('/api/v1/runs/{runId}/steps/{stepId}/logs', {
      params: { path: { runId: ids.timedOutRun, stepId: ids.timedOutStep } },
    })

    expect(data?.truncated).toBe(true)
    expect(data?.items.at(-1)?.time).toBeNull()
    const other = await api.GET('/api/v1/runs/{runId}/steps/{stepId}/logs', {
      params: { path: { runId: ids.failedRun, stepId: ids.failedStep } },
    })
    expect(other.data?.truncated).toBe(false)
  })
})
