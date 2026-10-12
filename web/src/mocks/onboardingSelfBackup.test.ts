// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import createClient from 'openapi-fetch'
import { afterEach, describe, expect, test, vi } from 'vitest'
import type { paths } from '../api/schema'
import { resetMockState } from './state'

// The mocks of the self-backup steps as the wizard sees them: the typed client against the MSW handlers
// (docs/specs/web/onboarding-self-backup.feature, rule "Моки ведут себя как сервер", Рк18).
const api = createClient<paths>({ baseUrl: location.origin })
const CODE = 'ABCD-EFGH-JKMN-PQRS-TVWX-YZ01-2345'
const REPORTING_MS = 3000

afterEach(() => vi.useRealTimers())

/** A clean installation after the step admin: signed in, the self-backup step pending. */
async function afterAdminStep(options: { upgraded?: boolean; failFirstBackup?: boolean } = {}) {
  resetMockState({ signedIn: false, freshInstall: true, ...options })
  await api.POST('/api/v1/onboarding/setup-session', { body: { code: CODE } })
  await api.POST('/api/v1/onboarding/ca')
  await api.POST('/api/v1/onboarding/admin', { body: { password: 'mock-admin-pass' } })
}

const onboarding = async () => (await api.GET('/api/v1/onboarding')).data
const selfBackup = async () => (await api.GET('/api/v1/self-backup')).data
const command = (query: Record<string, string>) =>
  api.GET('/api/v1/self-backup/repository-command', { params: { query } })
const confirmKeys = () =>
  api.POST('/api/v1/self-backup/keys-confirmation', { body: { copiedOutsideServer: true } })

/** The neighbour reports a local repository asked for by the command, and the self-backup is bound to it. */
async function bound() {
  vi.useFakeTimers({ toFake: ['Date'] })
  const asked = (await command({ storage: 'local' })).data
  vi.advanceTimersByTime(REPORTING_MS)
  const name = asked?.repositoryName ?? ''
  await api.PUT('/api/v1/self-backup/repository', {
    body: { repositoryName: name, confirmLocalStorage: true },
  })
  return name
}

const runsOf = async () => (await api.POST('/api/v1/self-backup/runs')).data?.runs ?? []

describe('the clean-install mock after the step admin', () => {
  test('reports the self-backup step pending and the full restriction', async () => {
    await afterAdminStep()
    expect(await onboarding()).toMatchObject({
      steps: [
        { id: 'ca', state: 'done' },
        { id: 'admin', state: 'done' },
        { id: 'self_backup', state: 'pending' },
        { id: 'keys_confirmed', state: 'upcoming' },
      ],
      restriction: 'full',
    })
  })

  test('answers the issue of a token with 409 onboarding_incomplete', async () => {
    await afterAdminStep()
    const { response, error } = await api.POST('/api/v1/enrollment-tokens', { body: {} })
    expect(response.status).toBe(409)
    expect(error?.code).toBe('onboarding_incomplete')
  })

  test.each([
    ['POST /api/v1/sources', () => api.POST('/api/v1/sources', { body: sourceBody() })],
    ['DELETE a source', () => api.DELETE('/api/v1/sources/{sourceId}', params(UNKNOWN))],
    ['POST a run', () => api.POST('/api/v1/sources/{sourceId}/runs', params(UNKNOWN))],
    [
      'PUT a schedule',
      () =>
        api.PUT('/api/v1/sources/{sourceId}/schedule', {
          ...params(UNKNOWN),
          body: { cron: '0 3 * * *', timezone: 'UTC', enabled: true },
        }),
    ],
    ['revoke an agent', () => api.POST('/api/v1/agents/{agentId}/revoke', agentParams(UNKNOWN))],
  ])('refuses %s with onboarding_incomplete before anything else', async (_name, call) => {
    await afterAdminStep()
    const { response, error } = await call()
    expect(response.status).toBe(409)
    expect(error?.code).toBe('onboarding_incomplete')
  })

  test('reads are not restricted', async () => {
    await afterAdminStep()
    for (const path of ['/api/v1/agents', '/api/v1/sources', '/api/v1/runs', '/api/v1/overview']) {
      expect((await fetch(path)).status, path).toBe(200)
    }
  })

  test('the neighbour reports the repository 3 seconds after the command was asked for', async () => {
    await afterAdminStep()
    vi.useFakeTimers({ toFake: ['Date'] })
    const asked = (await command({ storage: 'local' })).data
    expect((await selfBackup())?.repositories.map((r) => r.name)).not.toContain(
      asked?.repositoryName,
    )

    vi.advanceTimersByTime(REPORTING_MS)

    const reported = (await selfBackup())?.repositories.find(
      (r) => r.name === asked?.repositoryName,
    )
    expect(reported).toMatchObject({ backend: 'local', local: true })
    expect(reported?.repositoryId).toMatch(/^[0-9a-f]{64}$/)
  })
})

const UNKNOWN = '0192f7a0-0000-7000-8000-00000000ffff'
const params = (sourceId: string) => ({ params: { path: { sourceId } } })
const agentParams = (agentId: string) => ({ params: { path: { agentId } } })
const sourceBody = () => ({
  name: 'x',
  agentId: UNKNOWN,
  plugin: 'files',
  repositoryName: 'r',
  config: {},
})

describe('the command of the mock', () => {
  test('is built like the server builds it', async () => {
    await afterAdminStep()
    const { data } = await command({
      storage: 's3',
      provider: 'aws',
      region: 'eu-central-1',
      bucket: 'acme-sard',
      path: 'prod',
    })
    expect(data).toMatchObject({ repositoryName: 'self', address: 's3:acme-sard/prod' })
    expect(data?.commands).toHaveLength(3)
    expect(data?.commands[0]).toBe('read -r -p "S3 access key ID: " SARD_S3_KEY_ID')
    expect(data?.commands[2]).toBe('docker compose restart self-agent')
  })

  test('refuses a parameter that does not fit with the field named', async () => {
    await afterAdminStep()
    const { response, error } = await command({
      storage: 's3',
      provider: 'aws',
      region: 'eu-central-1',
      bucket: 'Acme_Sard',
    })
    expect(response.status).toBe(422)
    expect(error).toMatchObject({ code: 'validation_failed', errors: [{ field: 'bucket' }] })
  })

  test('takes the first free name', async () => {
    await afterAdminStep()
    vi.useFakeTimers({ toFake: ['Date'] })
    await command({ storage: 'local' })
    vi.advanceTimersByTime(REPORTING_MS)
    expect((await command({ storage: 'local' })).data?.repositoryName).toBe('self-2')
  })
})

describe('the first backup of the mock', () => {
  test('both runs succeed 3 seconds after "back up now" and the step is done', async () => {
    await afterAdminStep()
    await bound()
    expect((await selfBackup())?.firstBackupDone).toBe(false)

    const runs = await runsOf()
    vi.advanceTimersByTime(REPORTING_MS)

    for (const run of runs) {
      const { data } = await api.GET('/api/v1/runs/{runId}', {
        params: { path: { runId: run.runId } },
      })
      expect(data?.status).toBe('succeeded')
    }
    expect((await selfBackup())?.firstBackupDone).toBe(true)
    expect((await onboarding())?.steps.map((s) => s.state)).toEqual([
      'done',
      'done',
      'done',
      'pending',
    ])
  })

  test('with the failure switched on the first run of the database fails with a message of the step', async () => {
    await afterAdminStep({ failFirstBackup: true })
    await bound()
    const first = await runsOf()
    vi.advanceTimersByTime(REPORTING_MS)

    const run = (
      await api.GET('/api/v1/runs/{runId}', { params: { path: { runId: first[0].runId } } })
    ).data
    expect(run?.status).toBe('failed')
    expect(run?.steps[0].message).toBeTruthy()
    expect((await selfBackup())?.firstBackupDone).toBe(false)

    await runsOf()
    vi.advanceTimersByTime(REPORTING_MS)
    expect((await selfBackup())?.firstBackupDone).toBe(true)
  })
})

describe('the key copy of the mock', () => {
  test('is refused before the first backups with 409 first_backup_pending', async () => {
    await afterAdminStep()
    await bound()
    const { response, error } = await confirmKeys()
    expect(response.status).toBe(409)
    expect(error?.code).toBe('first_backup_pending')
  })

  test('is refused without a binding with 409 self_backup_not_configured', async () => {
    await afterAdminStep()
    expect((await confirmKeys()).error?.code).toBe('self_backup_not_configured')
  })

  test.each([{}, { copiedOutsideServer: false }])('is refused for the body %j', async (body) => {
    await afterAdminStep()
    await bound()
    await runsOf()
    vi.advanceTimersByTime(REPORTING_MS)
    const { response, error } = await api.POST('/api/v1/self-backup/keys-confirmation', {
      body: body as { copiedOutsideServer: boolean },
    })
    expect(response.status).toBe(422)
    expect(error).toMatchObject({ errors: [{ field: 'copiedOutsideServer' }] })
  })

  test('after the first backups completes the onboarding and opens the tokens', async () => {
    await afterAdminStep()
    await bound()
    await runsOf()
    vi.advanceTimersByTime(REPORTING_MS)

    expect((await confirmKeys()).response.status).toBe(204)

    expect(await onboarding()).toMatchObject({ restriction: 'none' })
    expect((await onboarding())?.steps.every((s) => s.state === 'done')).toBe(true)
    expect((await api.POST('/api/v1/enrollment-tokens', { body: {} })).response.status).toBe(201)
  })
})

describe('the recovery card and the location of the mock', () => {
  test('is unavailable before the binding', async () => {
    await afterAdminStep()
    const { response, error } = await api.GET('/api/v1/self-backup/recovery-card')
    expect(response.status).toBe(409)
    expect(error?.code).toBe('self_backup_not_configured')
  })

  test('holds the address the binding recorded, and no secret', async () => {
    await afterAdminStep()
    vi.useFakeTimers({ toFake: ['Date'] })
    const asked = (await command({ storage: 'local' })).data
    vi.advanceTimersByTime(REPORTING_MS)
    await api.PUT('/api/v1/self-backup/repository', {
      body: {
        repositoryName: asked?.repositoryName ?? '',
        confirmLocalStorage: true,
        location: asked?.address,
      },
    })

    const { data } = await api.GET('/api/v1/self-backup/recovery-card')

    expect(data).toMatchObject({
      repositoryName: asked?.repositoryName,
      backend: 'local',
      location: asked?.address,
    })
    expect(Object.keys(data ?? {}).sort()).toEqual([
      'backend',
      'caFingerprint',
      'generatedAt',
      'location',
      'recoveryProcedure',
      'repositoryId',
      'repositoryName',
      'sardVersion',
    ])
  })

  test('refuses an address with credentials at the field location', async () => {
    await afterAdminStep()
    vi.useFakeTimers({ toFake: ['Date'] })
    const asked = (await command({ storage: 'local' })).data
    vi.advanceTimersByTime(REPORTING_MS)
    const { response, error } = await api.PUT('/api/v1/self-backup/repository', {
      body: {
        repositoryName: asked?.repositoryName ?? '',
        confirmLocalStorage: true,
        location: 'sftp:backup:pw@backup.example.net:/srv/sard',
      },
    })
    expect(response.status).toBe(422)
    expect(error).toMatchObject({ errors: [{ field: 'location' }] })
  })
})

describe('the upgraded-installation mock', () => {
  test('reports the restriction new_agents and refuses only the issue of tokens', async () => {
    resetMockState({ signedIn: true, upgraded: true })
    expect(await onboarding()).toMatchObject({ restriction: 'new_agents' })
    const token = await api.POST('/api/v1/enrollment-tokens', { body: {} })
    expect(token.error?.code).toBe('onboarding_incomplete')
    const run = await api.POST('/api/v1/sources/{sourceId}/runs', params(UNKNOWN))
    expect(run.error?.code).not.toBe('onboarding_incomplete')
  })
})
