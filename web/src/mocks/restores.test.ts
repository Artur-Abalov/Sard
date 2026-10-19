// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import createClient from 'openapi-fetch'
import { describe, expect, test } from 'vitest'
import filesRestoreSchemaOfAgent from '../../../agent/plugins/files/restore_schema.json'
import postgresqlRestoreSchemaOfAgent from '../../../agent/plugins/postgresql/restore_schema.json'
import postgresqlSchemaOfAgent from '../../../agent/plugins/postgresql/schema.json'
import type { paths } from '../api/schema'
import { ids, MOCK_PASSWORD } from './fixtures'
import { postgresqlRestoreSchema, postgresqlSchema } from './postgresqlSchemas'
import { filesRestoreSchema } from './restoreSchema'
import { state } from './state'

// The restore endpoints as the server answers them (docs/specs/server/console-restore.feature).
const api = createClient<paths>({ baseUrl: location.origin })

const SNAPSHOT = '4f1c2a9e'
const PARTIAL = 'a1b2c3'
const path = (snapshotId: string, sourceId: string = ids.etcSource) => ({
  params: { path: { sourceId, snapshotId } },
})
const body = (agentId: string, config: Record<string, unknown>) => ({ agentId, config })
const good = { destination: 'etc/copy' }

async function signIn() {
  await api.POST('/api/v1/session', { body: { password: MOCK_PASSWORD } })
}

function must<T>(result: { data?: T; response: Response }): T {
  if (result.data === undefined) throw new Error(`${result.response.status} ${result.response.url}`)
  return result.data
}

describe('the restore schema of the mocks', () => {
  test('is the one the agent announces', () => {
    expect(filesRestoreSchema).toEqual(filesRestoreSchemaOfAgent)
  })
})

describe('the schemas of postgresql in the mocks', () => {
  test('are the ones the agent announces', () => {
    expect(postgresqlSchema).toEqual(postgresqlSchemaOfAgent)
    expect(postgresqlRestoreSchema).toEqual(postgresqlRestoreSchemaOfAgent)
  })
})

describe('restore options', () => {
  test('need a session', async () => {
    const { response } = await api.GET(
      '/api/v1/sources/{sourceId}/snapshots/{snapshotId}/restore-options',
      path(SNAPSHOT),
    )
    expect(response.status).toBe(401)
  })

  test('name the snapshot, the agents in the server order and the ones that cannot', async () => {
    await signIn()
    const options = must(
      await api.GET(
        '/api/v1/sources/{sourceId}/snapshots/{snapshotId}/restore-options',
        path(SNAPSHOT),
      ),
    )
    expect(options.snapshot).toMatchObject({
      snapshotId: SNAPSHOT,
      totalBytes: 52_428_800,
      sourceName: 'db1 /etc',
      plugin: 'files',
    })
    expect(options.targets.map((t) => [t.hostname, t.sourceAgent, t.online])).toEqual([
      ['db1.example.com', true, true],
      ['clone4.example.com', false, false],
    ])
    expect(options.ineligible).toEqual([
      { agentId: ids.webAgent, hostname: 'web2.example.com', reason: 'agent_outdated' },
    ])
    expect(options.attachHint.repositoryName).toBe('local')
    expect(options.attachHint.command).toContain('repo add local')
  })

  test('start the form from the destination of the source and the time of the snapshot', async () => {
    await signIn()
    const options = must(
      await api.GET(
        '/api/v1/sources/{sourceId}/snapshots/{snapshotId}/restore-options',
        path(SNAPSHOT),
      ),
    )
    const [first] = options.targets
    expect(first.defaults).toEqual({ destination: 'db1%20%2Fetc/20260925T210230Z' })
    expect(first.restoreDir).toBe('/var/lib/sard-agent/restore')
    expect(first.restoreConfigSchema).toEqual(filesRestoreSchema)
  })

  test('an unknown snapshot is 404', async () => {
    await signIn()
    const { response } = await api.GET(
      '/api/v1/sources/{sourceId}/snapshots/{snapshotId}/restore-options',
      path('ffffffff'),
    )
    expect(response.status).toBe(404)
  })
})

describe('the preview', () => {
  const preview = (agentId: string, config: Record<string, unknown>, snapshot = SNAPSHOT) =>
    api.POST('/api/v1/sources/{sourceId}/snapshots/{snapshotId}/restores/preview', {
      ...path(snapshot),
      body: body(agentId, config),
    })

  test('gives the place and the warning about owners', async () => {
    await signIn()
    const answer = must(await preview(ids.dbAgent, good))
    expect(answer).toMatchObject({
      sourceName: 'db1 /etc',
      snapshotId: SNAPSHOT,
      agentId: ids.dbAgent,
      hostname: 'db1.example.com',
      agentOnline: true,
      target: '/var/lib/sard-agent/restore/etc/copy',
      paths: [],
      restoreGlobals: false,
      warnings: ['owners_not_restored'],
    })
  })

  test('warns first that an offline agent will start later', async () => {
    await signIn()
    const answer = must(await preview(ids.duplicateAgent, good))
    expect(answer.agentOnline).toBe(false)
    expect(answer.target).toBe('/srv/restore/etc/copy')
    expect(answer.warnings).toEqual(['agent_offline', 'owners_not_restored'])
  })

  test.each([
    ['a destination that leaves the directory', { destination: '../etc' }, 'config/destination'],
    ['a missing destination', {}, 'config/destination'],
    ['a destination that is not text', { destination: 5 }, 'config/destination'],
    ['a relative path', { destination: 'a', paths: ['etc'] }, 'config/paths/0'],
    ['a path with ..', { destination: 'a', paths: ['/etc/../x'] }, 'config/paths/0'],
    ['a property the schema does not know', { destination: 'a', mode: 'x' }, 'config/mode'],
  ])('refuses %s at its field', async (_name, config, field) => {
    await signIn()
    const { response, error } = await preview(ids.dbAgent, config)
    expect(response.status).toBe(422)
    expect(error).toMatchObject({ code: 'validation_failed', errors: [{ field }] })
  })

  test('creates nothing', async () => {
    await signIn()
    await preview(ids.dbAgent, good)
    expect(state.runs.some((r) => r.kind === 'restore')).toBe(false)
  })

  test('checks in the order of the server: snapshot, restorable, agent, revoked, repository, plugin', async () => {
    await signIn()
    expect((await preview(ids.dbAgent, good, 'ffffffff')).response.status).toBe(404)
    const partial = await preview(ids.dbAgent, good, PARTIAL)
    expect([partial.response.status, partial.error?.code]).toEqual([422, 'snapshot_not_restorable'])
    const unknown = await preview('0192f7a0-0000-7000-8000-00000000ffff', good)
    expect([unknown.response.status, unknown.error]).toMatchObject([
      422,
      { errors: [{ field: 'agentId' }] },
    ])
    const revoked = await preview(ids.revokedAgent, good)
    expect([revoked.response.status, revoked.error?.code]).toEqual([409, 'agent_revoked'])
    const other = await preview(ids.selfAgent, good)
    expect([other.response.status, other.error?.code]).toEqual([409, 'unknown_repository'])
    const outdated = await preview(ids.webAgent, good)
    expect([outdated.response.status, outdated.error?.code]).toEqual([409, 'unknown_plugin'])
  })
})

describe('starting a restore', () => {
  const start = (agentId: string, config: Record<string, unknown>, snapshot = SNAPSHOT) =>
    api.POST('/api/v1/sources/{sourceId}/snapshots/{snapshotId}/restores', {
      ...path(snapshot),
      body: body(agentId, config),
    })

  test('queues a restore run of the administrator on the chosen agent', async () => {
    await signIn()
    const run = must(await start(ids.dbAgent, good))
    expect(run).toMatchObject({
      kind: 'restore',
      status: 'queued',
      trigger: 'manual',
      requestedBy: 'admin',
      sourceId: ids.etcSource,
      agentId: ids.dbAgent,
      restore: {
        snapshotId: SNAPSHOT,
        repositoryName: 'local',
        config: good,
        target: '/var/lib/sard-agent/restore/etc/copy',
      },
    })
    expect(run.steps.map((s) => [s.action, s.status, s.restore])).toEqual([
      ['restore', 'queued', null],
    ])
    expect(
      must(await api.GET('/api/v1/runs/{runId}', { params: { path: { runId: run.id } } })).id,
    ).toBe(run.id)
  })

  test('is not held back by a backup of the source that is going', async () => {
    await signIn()
    expect(state.runs.some((r) => r.sourceId === ids.etcSource && r.status === 'running')).toBe(
      true,
    )
    expect((await start(ids.dbAgent, good)).response.status).toBe(201)
    expect((await start(ids.dbAgent, good)).response.status).toBe(201)
  })

  test('refuses what the preview refuses', async () => {
    await signIn()
    expect((await start(ids.dbAgent, { destination: '../etc' })).response.status).toBe(422)
    expect((await start(ids.revokedAgent, good)).response.status).toBe(409)
    expect(state.runs.some((r) => r.kind === 'restore')).toBe(false)
  })

  test('shows in the list of restores and not in the list of backups', async () => {
    await signIn()
    const run = must(await start(ids.dbAgent, good))
    const restores = must(await api.GET('/api/v1/runs', { params: { query: { kind: 'restore' } } }))
    expect(restores.items.map((r) => r.id)).toEqual([run.id])
    const backups = must(await api.GET('/api/v1/runs', { params: { query: { kind: 'backup' } } }))
    expect(backups.items.some((r) => r.id === run.id)).toBe(false)
    const all = must(await api.GET('/api/v1/runs'))
    expect(all.items.some((r) => r.id === run.id)).toBe(true)
  })
})

// R2b: the restore of a database as the server answers it (ВК32, ВК32a, ВК32b).
describe('a restore of a database', () => {
  const DATABASE = '9d3e5f7a'
  const SQ = { snapshot: DATABASE, source: ids.appSource }
  const restored = {
    host: 'other-db',
    user: 'restorer',
    password_ref: 'pg-password',
    database: 'app_restored_20261011',
  }
  const at = (snapshot: string = DATABASE) => path(snapshot, ids.appSource)
  const preview = (agentId: string, config: Record<string, unknown>) =>
    api.POST('/api/v1/sources/{sourceId}/snapshots/{snapshotId}/restores/preview', {
      ...at(),
      body: body(agentId, config),
    })

  test('the snapshot of a postgresql source is restorable', async () => {
    await signIn()
    const snapshot = state.snapshots.find((s) => s.snapshotId === SQ.snapshot)
    expect(snapshot).toMatchObject({ restorable: true, restoreBlockedReason: null })
  })

  test('the form starts from the connection of the backup and a new name', async () => {
    await signIn()
    const options = must(
      await api.GET('/api/v1/sources/{sourceId}/snapshots/{snapshotId}/restore-options', at()),
    )
    const [first] = options.targets
    expect(first.defaults).toEqual({
      host: 'db1.example.com',
      port: 5432,
      user: 'backup',
      password_ref: 'pg-password',
      tls_mode: 'disable',
      database: 'app_restored_20261011',
      restore_globals: false,
    })
    expect(first.restoreConfigSchema).toEqual(postgresqlRestoreSchema)
  })

  test('a preview names the database and warns that owners are not restored', async () => {
    await signIn()
    const answer = must(await preview(ids.dbAgent, restored))
    expect(answer).toMatchObject({
      target: 'app_restored_20261011',
      paths: [],
      restoreGlobals: false,
      warnings: ['pg_owners_not_restored'],
    })
  })

  test('the global objects warn that the cluster changes, not about owners', async () => {
    await signIn()
    const answer = must(await preview(ids.dbAgent, { ...restored, restore_globals: true }))
    expect(answer.restoreGlobals).toBe(true)
    expect(answer.warnings).toEqual(['globals_change_cluster'])
  })

  test.each([
    ['the host of the backup', { host: 'DB1.example.com' }, true],
    ['another host', { host: 'db9' }, false],
    ['the host with another port', { host: 'db1.example.com', port: 5433 }, false],
    ['the host with the port of the backup', { host: 'db1.example.com', port: 5432 }, true],
  ])('%s: the same cluster is told by the agent and the address', async (_name, extra, same) => {
    await signIn()
    const answer = must(await preview(ids.dbAgent, { ...restored, ...extra }))
    expect(answer.warnings.includes('pg_same_cluster')).toBe(same)
  })

  test('another agent is not the same cluster, whatever the address', async () => {
    await signIn()
    const other = state.agents.find((a) => a.id === ids.duplicateAgent)
    other?.plugins.push({
      name: 'postgresql',
      version: '0.1.0',
      actions: ['backup', 'restore'],
      configSchema: postgresqlSchema as never,
    })
    other?.secretNames.push('pg-password')
    const answer = must(await preview(ids.duplicateAgent, { ...restored, host: 'db1.example.com' }))
    expect(answer.warnings).not.toContain('pg_same_cluster')
  })

  test('the same cluster does not stop the restore', async () => {
    await signIn()
    const config = { ...restored, host: 'db1.example.com' }
    expect(must(await preview(ids.dbAgent, config)).warnings).toContain('pg_same_cluster')
    const run = must(
      await api.POST('/api/v1/sources/{sourceId}/snapshots/{snapshotId}/restores', {
        ...at(),
        body: body(ids.dbAgent, config),
      }),
    )
    expect(run).toMatchObject({ kind: 'restore', status: 'queued' })
  })

  test('a run keeps the database as its target', async () => {
    await signIn()
    const run = must(
      await api.POST('/api/v1/sources/{sourceId}/snapshots/{snapshotId}/restores', {
        ...at(),
        body: body(ids.dbAgent, restored),
      }),
    )
    expect(run.restore).toMatchObject({ target: 'app_restored_20261011', config: restored })
    expect(run.steps[0]).toMatchObject({ plugin: 'postgresql', action: 'restore' })
  })

  test.each([
    ['a missing database', { ...restored, database: undefined }, 'config/database'],
    ['a database of 64 characters', { ...restored, database: 'a'.repeat(64) }, 'config/database'],
    ['a password in the request', { ...restored, password: 'hunter2' }, 'config'],
    [
      'a secret the agent does not have',
      { ...restored, password_ref: 'nope' },
      'config/password_ref',
    ],
    [
      'globals that are no boolean',
      { ...restored, restore_globals: 'yes' },
      'config/restore_globals',
    ],
    ['a port that is no number', { ...restored, port: 'x' }, 'config/port'],
    ['a TLS mode of no kind', { ...restored, tls_mode: 'maybe' }, 'config/tls_mode'],
  ])('refuses %s at its field', async (_name, config, field) => {
    await signIn()
    const { response, error } = await preview(ids.dbAgent, config)
    expect(response.status).toBe(422)
    expect(error).toMatchObject({ code: 'validation_failed', errors: [{ field }] })
  })

  test('does not repeat the password it refuses', async () => {
    await signIn()
    const { error } = await preview(ids.dbAgent, { ...restored, password: 'hunter2' })
    expect(JSON.stringify(error)).not.toContain('hunter2')
  })
})
