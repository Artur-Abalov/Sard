// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { client } from '../api/client'
import type { components } from '../api/schema'
import { fieldFailures } from '../errors'
import { timeToCron } from './schedule'

type Schemas = components['schemas']

export type Storage = 's3' | 'sftp' | 'local'
export type Provider = 'aws' | 'b2' | 'custom'

// What the owner types to describe the storage (Рк11). None of it is a secret: the key ID and the secret key
// are typed in the terminal of the server. The console judges nothing but whether a field is empty (Рк12):
// the format is the server's to say, with a 422 that names the field.
export interface StorageForm {
  storage: Storage
  provider: Provider
  region: string
  endpoint: string
  bucket: string
  path: string
  user: string
  host: string
  port: string
  hostKeyFingerprint: string
}

// S3 is the storage chosen by default (D10).
export const emptyForm: StorageForm = {
  storage: 's3',
  provider: 'aws',
  region: '',
  endpoint: '',
  bucket: '',
  path: '',
  user: '',
  host: '',
  port: '',
  hostKeyFingerprint: '',
}

// The fields each storage asks for, required ones first in the order the server reads them.
const FIELDS: Record<
  Storage | 'aws' | 'b2' | 'custom',
  { required: string[]; optional: string[] }
> = {
  aws: { required: ['region', 'bucket'], optional: ['path'] },
  b2: { required: ['region', 'bucket'], optional: ['path'] },
  custom: { required: ['endpoint', 'bucket'], optional: ['path'] },
  s3: { required: [], optional: [] },
  sftp: { required: ['user', 'host', 'path'], optional: ['port', 'hostKeyFingerprint'] },
  local: { required: [], optional: [] },
}

function fieldsOf(form: StorageForm) {
  return form.storage === 's3' ? FIELDS[form.provider] : FIELDS[form.storage]
}

function valueOf(form: StorageForm, field: string): string {
  return form[field as keyof StorageForm]
}

/** The query of the request for the command: the form as it is, only what the storage asks for and what is filled. */
export function commandQuery(form: StorageForm): Record<string, string> {
  const query: Record<string, string> = { storage: form.storage }
  if (form.storage === 's3') query.provider = form.provider
  const { required, optional } = fieldsOf(form)
  for (const field of [...required, ...optional]) {
    const value = valueOf(form, field)
    if (value !== '') query[field] = value
  }
  return orderedLikeForm(query)
}

// The server reads the parameters in the order of the form; the URL keeps it.
const ORDER = [
  'storage',
  'provider',
  'region',
  'endpoint',
  'bucket',
  'path',
  'user',
  'host',
  'port',
  'hostKeyFingerprint',
]

function orderedLikeForm(query: Record<string, string>): Record<string, string> {
  return Object.fromEntries(
    ORDER.filter((field) => field in query).map((field) => [field, query[field]]),
  )
}

/** The button that shows the command: the required fields are filled, a local storage is understood (Рк11). */
export function canShowCommand(form: StorageForm, localUnderstood: boolean): boolean {
  if (form.storage === 'local') return localUnderstood
  return fieldsOf(form).required.every((field) => valueOf(form, field) !== '')
}

export type CommandResult =
  | { kind: 'ok'; command: Schemas['RepositoryCommand'] }
  | { kind: 'fields'; fields: string[] }
  | { kind: 'agent_missing' }
  | { kind: 'unavailable' }

function refusalOf(error: unknown): CommandResult {
  const code =
    typeof error === 'object' && error !== null && 'code' in error ? String(error.code) : null
  if (code === 'self_agent_missing') return { kind: 'agent_missing' }
  const fields = fieldFailures(error).map((failure) => failure.field)
  return fields.length > 0 ? { kind: 'fields', fields } : { kind: 'unavailable' }
}

/** GET /api/v1/self-backup/repository-command with the form as it is. */
export async function fetchCommand(form: StorageForm): Promise<CommandResult> {
  try {
    const { data, error } = await client.GET('/api/v1/self-backup/repository-command', {
      params: { query: commandQuery(form) },
    })
    return data === undefined ? refusalOf(error) : { kind: 'ok', command: data }
  } catch {
    return { kind: 'unavailable' }
  }
}

/**
 * The binding to ask for once the neighbour reports the repository of the command with an id; null while it does not
 * (an uninitialised repository is not bound). A local repository is bound with the owner's confirmation (Рк11, OQ-203).
 */
export function bindingInput(
  command: Pick<Schemas['RepositoryCommand'], 'repositoryName' | 'address'>,
  state: Pick<Schemas['SelfBackup'], 'repositories'>,
  localConfirmed: boolean,
): Schemas['SelfBackupRepositoryInput'] | null {
  const repository = state.repositories.find((r) => r.name === command.repositoryName)
  if (repository === undefined || repository.repositoryId === null) return null
  return {
    repositoryName: repository.name,
    location: command.address,
    confirmLocalStorage: repository.local && localConfirmed,
  }
}

export type BindResult = 'waiting' | 'bound' | 'local_storage_unconfirmed' | 'failed'

/** One look at the neighbour: binds the repository of the command when it is there. */
export async function bindWhenReady(
  command: Pick<Schemas['RepositoryCommand'], 'repositoryName' | 'address'>,
  localConfirmed: boolean,
): Promise<BindResult> {
  try {
    const state = (await client.GET('/api/v1/self-backup')).data
    const input = state === undefined ? null : bindingInput(command, state, localConfirmed)
    if (input === null) return 'waiting'
    const { response, error } = await client.PUT('/api/v1/self-backup/repository', { body: input })
    return bindResult(response, error)
  } catch {
    return 'failed'
  }
}

function bindResult(response: Response, error: unknown): BindResult {
  if (response.ok) return 'bound'
  const code =
    typeof error === 'object' && error !== null && 'code' in error ? String(error.code) : null
  return code === 'local_storage_unconfirmed' ? code : 'failed'
}

/** Binds a repository the neighbour already has, without an address (the owner closed the tab after the command). */
export async function attachRepository(
  repository: Pick<Schemas['SelfBackupRepository'], 'name' | 'local'>,
  localConfirmed: boolean,
): Promise<BindResult> {
  try {
    const { response, error } = await client.PUT('/api/v1/self-backup/repository', {
      body: {
        repositoryName: repository.name,
        confirmLocalStorage: repository.local && localConfirmed,
      },
    })
    return bindResult(response, error)
  } catch {
    return 'failed'
  }
}

/**
 * Puts one cron "M H * * *" to both system sources (Рк12). False when the time is no time or any of the
 * requests was refused: the schedule is then not (wholly) saved.
 */
export async function saveSchedule(
  state: Pick<Schemas['SelfBackup'], 'sources'>,
  time: string,
  timezone: string,
): Promise<boolean> {
  const cron = timeToCron(time)
  if (cron === null) return false
  try {
    const results = await Promise.all(
      state.sources.map((source) =>
        client.PUT('/api/v1/sources/{sourceId}/schedule', {
          params: { path: { sourceId: source.sourceId } },
          body: { cron, timezone, enabled: true },
        }),
      ),
    )
    return results.every(({ response }) => response.ok)
  } catch {
    return false
  }
}

export type FirstBackup = { ok: true; runs: Schemas['SelfBackupRunStarted'][] } | { ok: false }

/** POST /api/v1/self-backup/runs: the first backup and its retry are the same request (Рк13). */
export async function startFirstBackup(): Promise<FirstBackup> {
  try {
    const { data } = await client.POST('/api/v1/self-backup/runs')
    return data === undefined ? { ok: false } : { ok: true, runs: data.runs }
  } catch {
    return { ok: false }
  }
}

export type KeysResult = 'done' | 'first_backup_pending' | 'failed'

/** POST /api/v1/self-backup/keys-confirmation with the owner's mark (Рк14). */
export async function confirmKeysCopy(): Promise<KeysResult> {
  try {
    const { response, error } = await client.POST('/api/v1/self-backup/keys-confirmation', {
      body: { copiedOutsideServer: true },
    })
    if (response.ok) return 'done'
    const code =
      typeof error === 'object' && error !== null && 'code' in error ? String(error.code) : null
    return code === 'first_backup_pending' ? code : 'failed'
  } catch {
    return 'failed'
  }
}
