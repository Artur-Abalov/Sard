// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import type { components } from '../api/schema'
import { http } from '../mocks/http'
import { server } from '../mocks/node'
import { noSession, PROBLEM, problem } from '../mocks/problems'
import {
  attachRepository,
  bindingInput,
  bindWhenReady,
  canShowCommand,
  commandQuery,
  confirmKeysCopy,
  emptyForm,
  fetchCommand,
  saveSchedule,
  startFirstBackup,
  type StorageForm,
} from './selfBackup'

type Schemas = components['schemas']

const aws: StorageForm = {
  ...emptyForm,
  storage: 's3',
  provider: 'aws',
  region: 'eu-central-1',
  bucket: 'acme-sard',
  path: 'prod',
}

const command: Schemas['RepositoryCommand'] = {
  repositoryName: 'self',
  address: 's3:acme-sard/prod',
  commands: ['one', 'two', 'three'],
}

function repositories(
  ...items: Partial<Schemas['SelfBackupRepository']>[]
): Schemas['SelfBackupRepository'][] {
  return items.map((item) => ({
    name: 'self',
    backend: 's3',
    repositoryId: 'abc',
    local: false,
    ...item,
  }))
}

function selfBackupState(
  items: Schemas['SelfBackupRepository'][],
  extra: Partial<Schemas['SelfBackup']> = {},
): Schemas['SelfBackup'] {
  return {
    configured: false,
    agentId: null,
    repository: null,
    boundAt: null,
    sources: [],
    status: 'not_configured',
    warnings: [],
    overdueSince: null,
    repositories: items,
    plan: [],
    firstBackupDone: false,
    keysConfirmedAt: null,
    passwordRevealCommand: null,
    ...extra,
  }
}

const reportRepositories = (items: Schemas['SelfBackupRepository'][]) =>
  server.use(
    http.get('/api/v1/self-backup', ({ response }) => response(200).json(selfBackupState(items))),
  )

/** The bodies of the PUT requests to the binding, in the order they came. */
function recordBindings() {
  const bodies: unknown[] = []
  server.use(
    http.put('/api/v1/self-backup/repository', async ({ request, response }) => {
      bodies.push(await request.json())
      return response(200).json(selfBackupState([], { configured: true }))
    }),
  )
  return bodies
}

// docs/specs/web/onboarding-self-backup.feature, rule "Шаг самобэкапа — выбор хранилища и команда для терминала сервера".
describe('the parameters of the command (Рк11)', () => {
  test('are sent as the form has them, in the order of the form', async () => {
    let seen = ''
    server.use(
      http.get('/api/v1/self-backup/repository-command', ({ request, response }) => {
        seen = new URL(request.url).search
        return response(200).json(command)
      }),
    )

    const result = await fetchCommand(aws)

    expect(result).toEqual({ kind: 'ok', command })
    expect(seen).toBe('?storage=s3&provider=aws&region=eu-central-1&bucket=acme-sard&path=prod')
  })

  test('hold only what the chosen storage asks for and what is filled', () => {
    const leftovers = { ...aws, user: 'root', host: 'h', port: '22', hostKeyFingerprint: 'x' }
    expect(commandQuery(leftovers)).toEqual({
      storage: 's3',
      provider: 'aws',
      region: 'eu-central-1',
      bucket: 'acme-sard',
      path: 'prod',
    })
    expect(commandQuery({ ...aws, path: '' })).not.toHaveProperty('path')
    expect(commandQuery({ ...emptyForm, storage: 'local' })).toEqual({ storage: 'local' })
    expect(
      commandQuery({
        ...emptyForm,
        storage: 's3',
        provider: 'custom',
        endpoint: 'https://minio.example.net:9000',
        bucket: 'acme-sard',
        region: 'ignored',
      }),
    ).toEqual({
      storage: 's3',
      provider: 'custom',
      endpoint: 'https://minio.example.net:9000',
      bucket: 'acme-sard',
    })
    expect(
      commandQuery({
        ...emptyForm,
        storage: 'sftp',
        user: 'backup',
        host: 'backup.example.net',
        port: '2222',
        path: '/srv/sard',
        hostKeyFingerprint: 'SHA256:abc',
      }),
    ).toEqual({
      storage: 'sftp',
      user: 'backup',
      host: 'backup.example.net',
      port: '2222',
      path: '/srv/sard',
      hostKeyFingerprint: 'SHA256:abc',
    })
  })

  test('S3 is the storage chosen by default (D10)', () => {
    expect(emptyForm.storage).toBe('s3')
    expect(emptyForm.provider).toBe('aws')
  })
})

describe('the button that shows the command (Рк11: forms only)', () => {
  test.each([
    ['an empty S3 form', emptyForm, false, false],
    ['an AWS form with its region and bucket', aws, false, true],
    ['an AWS form without the region', { ...aws, region: '' }, false, false],
    ['an AWS form without the bucket', { ...aws, bucket: '' }, false, false],
    [
      'an own S3 without its address',
      { ...emptyForm, provider: 'custom' as const, bucket: 'b' },
      false,
      false,
    ],
    [
      'an own S3 with its address and bucket',
      { ...emptyForm, provider: 'custom' as const, endpoint: 'http://minio:9000', bucket: 'b' },
      false,
      true,
    ],
    ['an empty SFTP form', { ...emptyForm, storage: 'sftp' as const }, false, false],
    [
      'an SFTP form with user, host and path',
      { ...emptyForm, storage: 'sftp' as const, user: 'u', host: 'h', path: '/p' },
      false,
      true,
    ],
    [
      'the local storage without the understanding',
      { ...emptyForm, storage: 'local' as const },
      false,
      false,
    ],
    [
      'the local storage with the understanding',
      { ...emptyForm, storage: 'local' as const },
      true,
      true,
    ],
  ])('%s', (_name, form, understood, enabled) => {
    expect(canShowCommand(form, understood)).toBe(enabled)
  })

  test('the format of a value is not judged: a bucket with a bad name is still sent', () => {
    expect(canShowCommand({ ...aws, bucket: 'Acme;reboot' }, false)).toBe(true)
  })
})

describe('the answers to the request for the command', () => {
  const answer = (status: 422 | 503, error: unknown) =>
    server.use(
      http.get('/api/v1/self-backup/repository-command', ({ response }) =>
        status === 422
          ? response(422).json(error as Schemas['ValidationProblem'], PROBLEM)
          : new Response(null, { status: 503 }),
      ),
    )

  test('a refused parameter names the field', async () => {
    answer(422, {
      ...problem(422, 'Unprocessable Content', 'validation_failed'),
      errors: [{ field: 'bucket', message: 'bad' }],
    })
    expect(await fetchCommand(aws)).toEqual({ kind: 'fields', fields: ['bucket'] })
  })

  test('the built-in agent missing is told apart', async () => {
    answer(422, { ...problem(422, 'Unprocessable Content', 'self_agent_missing'), errors: [] })
    expect(await fetchCommand(aws)).toEqual({ kind: 'agent_missing' })
  })

  test('anything else is the server being unavailable', async () => {
    answer(503, null)
    expect(await fetchCommand(aws)).toEqual({ kind: 'unavailable' })
  })
})

// rule "Мастер сам дожидается репозитория и привязывает его"
describe('binding the repository the neighbour reported (Рк11, OQ-203)', () => {
  test('a repository of the command with an id is bound with the address of the command', async () => {
    reportRepositories(repositories({ name: 'self', repositoryId: 'abc' }))
    const bodies = recordBindings()

    const result = await bindWhenReady(command, false)

    expect(result).toBe('bound')
    expect(bodies).toEqual([
      { repositoryName: 'self', location: 's3:acme-sard/prod', confirmLocalStorage: false },
    ])
  })

  test('a repository that is not initialised is not bound', async () => {
    reportRepositories(repositories({ name: 'self', repositoryId: null }))
    const bodies = recordBindings()

    expect(await bindWhenReady(command, false)).toBe('waiting')
    expect(bodies).toEqual([])
  })

  test('a repository of another name is not bound', async () => {
    reportRepositories(repositories({ name: 'other' }))
    const bodies = recordBindings()

    expect(await bindWhenReady(command, false)).toBe('waiting')
    expect(bodies).toEqual([])
  })

  test('a local repository is bound with the confirmation of the owner', async () => {
    reportRepositories(repositories({ name: 'self', backend: 'local', local: true }))
    const bodies = recordBindings()

    await bindWhenReady({ ...command, address: '/var/lib/sard-self/self' }, true)

    expect(bodies).toEqual([
      {
        repositoryName: 'self',
        location: '/var/lib/sard-self/self',
        confirmLocalStorage: true,
      },
    ])
  })

  test('the server asking for the confirmation of a local storage is told apart', async () => {
    reportRepositories(repositories({ name: 'self', backend: 'local', local: true }))
    server.use(
      http.put('/api/v1/self-backup/repository', ({ response }) =>
        response(422).json(
          {
            ...problem(422, 'Unprocessable Content', 'local_storage_unconfirmed'),
            errors: [{ field: 'confirmLocalStorage', message: 'must be true' }],
          },
          PROBLEM,
        ),
      ),
    )

    expect(await bindWhenReady(command, false)).toBe('local_storage_unconfirmed')
  })

  test('another refusal or no answer is a failure', async () => {
    reportRepositories(repositories({ name: 'self' }))
    server.use(
      http.put('/api/v1/self-backup/repository', () => new Response(null, { status: 503 })),
    )
    expect(await bindWhenReady(command, false)).toBe('failed')
  })

  test('the input holds no confirmation for a storage that is not local, whatever the owner said', () => {
    const state = selfBackupState(repositories({ name: 'self' }))
    expect(bindingInput(command, state, true)).toEqual({
      repositoryName: 'self',
      location: 's3:acme-sard/prod',
      confirmLocalStorage: false,
    })
  })
})

describe('binding a repository the neighbour already has (Рк11)', () => {
  test('is bound without an address, a local one with the confirmation', async () => {
    const bodies = recordBindings()

    expect(await attachRepository({ name: 'self', local: false }, false)).toBe('bound')
    expect(await attachRepository({ name: 'disk', local: true }, true)).toBe('bound')

    expect(bodies).toEqual([
      { repositoryName: 'self', confirmLocalStorage: false },
      { repositoryName: 'disk', confirmLocalStorage: true },
    ])
  })

  test('a local one asks for the confirmation first', async () => {
    server.use(
      http.put('/api/v1/self-backup/repository', ({ response }) =>
        response(422).json(
          {
            ...problem(422, 'Unprocessable Content', 'local_storage_unconfirmed'),
            errors: [{ field: 'confirmLocalStorage', message: 'must be true' }],
          },
          PROBLEM,
        ),
      ),
    )
    expect(await attachRepository({ name: 'disk', local: true }, false)).toBe(
      'local_storage_unconfirmed',
    )
  })
})

// rule "Мастер показывает, что попадёт в бэкап, и расписание"
describe('saving the schedule (Рк12)', () => {
  const sources = [
    { role: 'self_database', sourceId: 'D' },
    { role: 'self_keys', sourceId: 'K' },
  ] as Schemas['SelfBackupSource'][]

  test('puts one cron to both system sources', async () => {
    const seen: { path: string; body: unknown }[] = []
    server.use(
      http.put('/api/v1/sources/{sourceId}/schedule', async ({ params, request, response }) => {
        seen.push({ path: params.sourceId, body: await request.json() })
        return response(401).json(noSession, PROBLEM)
      }),
    )

    await saveSchedule(selfBackupState([], { sources }), '01:30', 'Europe/Moscow')

    const body = { cron: '30 1 * * *', timezone: 'Europe/Moscow', enabled: true }
    expect(seen).toEqual([
      { path: 'D', body },
      { path: 'K', body },
    ])
  })

  test('is not sent for a text that is no time', async () => {
    const seen: string[] = []
    server.use(
      http.put('/api/v1/sources/{sourceId}/schedule', ({ params, response }) => {
        seen.push(params.sourceId)
        return response(401).json(noSession, PROBLEM)
      }),
    )
    expect(await saveSchedule(selfBackupState([], { sources }), '', 'UTC')).toBe(false)
    expect(seen).toEqual([])
  })
})

// rule "Первый бэкап обязателен, при ошибке — причина и повтор"
describe('the first backup (Рк13)', () => {
  test('starting and retrying are the same request', async () => {
    let posts = 0
    server.use(
      http.post('/api/v1/self-backup/runs', ({ response }) => {
        posts += 1
        return response(200).json({ runs: [] })
      }),
    )

    await startFirstBackup()
    await startFirstBackup()

    expect(posts).toBe(2)
  })

  test('a refusal is reported, not thrown', async () => {
    server.use(
      http.post('/api/v1/self-backup/runs', ({ response }) =>
        response(409).json(problem(409, 'Conflict', 'self_backup_not_configured'), PROBLEM),
      ),
    )
    expect(await startFirstBackup()).toEqual({ ok: false })
  })
})

// rule "Шаг копии ключей — команда пароля, карточка и явная отметка"
describe('the confirmation of the key copy (Рк14)', () => {
  test('sends the mark and is done on 204', async () => {
    let body: unknown = null
    server.use(
      http.post('/api/v1/self-backup/keys-confirmation', async ({ request, response }) => {
        body = await request.json()
        return response(204).empty()
      }),
    )

    expect(await confirmKeysCopy()).toBe('done')
    expect(body).toEqual({ copiedOutsideServer: true })
  })

  test('first_backup_pending sends the owner back to the self-backup step', async () => {
    server.use(
      http.post('/api/v1/self-backup/keys-confirmation', ({ response }) =>
        response(409).json(problem(409, 'Conflict', 'first_backup_pending'), PROBLEM),
      ),
    )
    expect(await confirmKeysCopy()).toBe('first_backup_pending')
  })

  test('anything else changes nothing and is a failure', async () => {
    server.use(
      http.post('/api/v1/self-backup/keys-confirmation', () => new Response(null, { status: 503 })),
    )
    expect(await confirmKeysCopy()).toBe('failed')
  })
})
