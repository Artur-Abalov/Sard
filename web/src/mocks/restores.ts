// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../api/schema'
import { postgresqlRestoreSchema } from './postgresqlSchemas'
import { filesRestoreSchema } from './restoreSchema'
import { problem } from './problems'
import { state } from './state'

type Schemas = components['schemas']
type Config = Record<string, unknown>
type Problem =
  { status: 409; body: Schemas['Problem'] } | { status: 422; body: Schemas['ValidationProblem'] }

export interface Choice {
  source: Schemas['Source']
  snapshot: Schemas['Snapshot']
  agent: Schemas['AgentDetails']
  repositoryName: string
  config: Config
  target: string
}

type Violation = { field: string; message: string }
type Warning = Schemas['RestoreWarningCode']

/** What the server knows of the restore of one plugin (ВК32a): the form, the place, the warnings. */
interface RestorePlugin {
  /** The restore goes into a directory below the restore directory of the host, not a database. */
  intoDirectory: boolean
  schema: Schemas['RestoreTarget']['restoreConfigSchema']
  defaults(source: Schemas['Source'], snapshot: Schemas['Snapshot']): Config
  configError(config: Config, agent: Schemas['AgentDetails']): Violation | null
  target(agent: Schemas['AgentDetails'], config: Config): string
  paths(config: Config): string[]
  restoreGlobals(config: Config): boolean
  warnings(choice: Choice): Warning[]
}

const SAFE = /[A-Za-z0-9_-]/

// <source name>/<snapshot time>: the bytes of the name outside [A-Za-z0-9_-] as %XX, the time in UTC (ВК32).
export function defaultDestination(sourceName: string, createdAt: string): string {
  const name = [...new TextEncoder().encode(sourceName)]
    .map((byte) => {
      const char = String.fromCharCode(byte)
      return SAFE.test(char) ? char : `%${byte.toString(16).toUpperCase().padStart(2, '0')}`
    })
    .join('')
  const stamp = createdAt.replace(/[-:]/g, '').replace(/\.\d+/, '')
  return `${name}/${stamp}`
}

/** The source (deleted or not) and its snapshot with the restic id; undefined when either is missing. */
export function snapshotOf(sourceId: string, snapshotId: string) {
  const source = [...state.sources, ...state.deletedSources].find((s) => s.id === sourceId)
  const snapshot = state.snapshots.find(
    (s) => s.sourceId === sourceId && s.snapshotId === snapshotId,
  )
  return source && snapshot ? { source, snapshot } : undefined
}

/** Unrevoked agents of a restoring role (never a verifier) that hold the snapshot's repository. */
function holding(snapshot: Schemas['Snapshot']) {
  return state.agents.flatMap((agent) => {
    const repository = agent.repositories.find((r) => r.repositoryId === snapshot.repositoryId)
    return agent.revokedAt === null && agent.role !== 'verifier' && repository
      ? [{ agent, repository: repository.name }]
      : []
  })
}

function restores(agent: Schemas['AgentDetails'], plugin: string): boolean {
  return (
    agent.restoreDir !== null &&
    agent.plugins.some((p) => p.name === plugin && p.actions.includes('restore'))
  )
}

/** Who holds the repository of the snapshot and restores it, the source's agent first; who holds it and cannot. */
export function restoreOptionsOf(
  source: Schemas['Source'],
  snapshot: Schemas['Snapshot'],
): Schemas['RestoreOptions'] {
  const holders = holding(snapshot)
  const plugin = restorePlugins[source.plugin]
  const targets = holders
    .filter(({ agent }) => plugin !== undefined && restores(agent, source.plugin))
    .map(({ agent, repository }) => ({
      agentId: agent.id,
      hostname: agent.hostname,
      online: agent.status === 'online',
      sourceAgent: agent.id === snapshot.agentId,
      repositoryName: repository,
      restoreDir: plugin?.intoDirectory ? (agent.restoreDir ?? '') : null,
      restoreConfigSchema: plugin?.schema as Schemas['RestoreTarget']['restoreConfigSchema'],
      defaults: plugin?.defaults(source, snapshot) as Config,
    }))
    .sort(
      (a, b) =>
        Number(b.sourceAgent) - Number(a.sourceAgent) || a.hostname.localeCompare(b.hostname),
    )
  const ineligible = holders
    .filter(({ agent }) => plugin === undefined || !restores(agent, source.plugin))
    .map(({ agent }) => ({
      agentId: agent.id,
      hostname: agent.hostname,
      reason: agent.plugins.some((p) => p.name === source.plugin)
        ? ('agent_outdated' as const)
        : ('plugin_missing' as const),
    }))
    .sort((a, b) => a.hostname.localeCompare(b.hostname))
  return {
    snapshot: {
      snapshotId: snapshot.snapshotId,
      createdAt: snapshot.createdAt,
      totalBytes: snapshot.totalBytes,
      repositoryName: snapshot.repositoryName,
      sourceName: source.name,
      plugin: source.plugin,
    },
    targets,
    ineligible,
    attachHint: {
      repositoryName: snapshot.repositoryName,
      command: `sudo sard-agent repo add ${snapshot.repositoryName} <адрес хранилища> --password-from-file <файл с копией пароля>`,
    },
  }
}

function invalid(field: string, message: string): Problem {
  return {
    status: 422,
    body: {
      ...problem(422, 'Unprocessable Content', 'validation_failed'),
      status: 422,
      detail: message,
      errors: [{ field, message }],
    },
  }
}

const conflict = (code: Schemas['ErrorCode']): Problem =>
  ({
    status: 409,
    body: problem(409, 'Conflict', code),
  }) as Problem

const destinationSchema = filesRestoreSchema.properties.destination
const pathSchema = filesRestoreSchema.properties.paths

function destinationError(value: unknown): string | null {
  if (value === undefined) return 'is required'
  if (typeof value !== 'string') return 'must be a string'
  const bytes = new TextEncoder().encode(value).length
  const fits = bytes >= destinationSchema.minLength && bytes <= destinationSchema.maxLength
  return fits && new RegExp(destinationSchema.pattern).test(value)
    ? null
    : 'is not a valid destination'
}

function pathError(value: unknown, all: unknown[]): string | null {
  if (typeof value !== 'string') return 'must be a string'
  const literal = new RegExp(pathSchema.items.pattern).test(value)
  const climbs = new RegExp(pathSchema.items.not.pattern).test(value)
  if (!literal || climbs || value.length > pathSchema.items.maxLength) return 'is not a valid path'
  return all.indexOf(value) === all.lastIndexOf(value) ? null : 'must not repeat'
}

// The first thing the schema refuses, at its place: config/<property> or config/paths/<index>.
function configError(config: Config): { field: string; message: string } | null {
  const unknown = Object.keys(config).find((name) => !(name in filesRestoreSchema.properties))
  if (unknown !== undefined) return { field: `config/${unknown}`, message: 'is not allowed' }
  const destination = destinationError(config.destination)
  if (destination !== null) return { field: 'config/destination', message: destination }
  const paths = config.paths ?? []
  if (!Array.isArray(paths) || paths.length > pathSchema.maxItems) {
    return { field: 'config/paths', message: 'must be a list of at most 100 paths' }
  }
  const index = paths.findIndex((value) => pathError(value, paths) !== null)
  return index < 0
    ? null
    : { field: `config/paths/${index}`, message: pathError(paths[index], paths) ?? '' }
}

// The agent of the request: it exists, is not revoked, holds the repository and restores the plugin.
function holder(
  source: Schemas['Source'],
  snapshot: Schemas['Snapshot'],
  agentId: string | null,
): { agent: Schemas['AgentDetails']; repository: string } | Problem {
  const agent = state.agents.find((a) => a.id === agentId)
  if (agent === undefined) {
    return invalid('agentId', 'must name an agent of this tenant that can restore the snapshot')
  }
  if (agent.revokedAt !== null) return conflict('agent_revoked')
  const repository = agent.repositories.find((r) => r.repositoryId === snapshot.repositoryId)
  if (repository === undefined) return conflict('unknown_repository')
  return restorePlugins[source.plugin] !== undefined && restores(agent, source.plugin)
    ? { agent, repository: repository.name }
    : conflict('unknown_plugin')
}

/**
 * The request checked in the order of the server (ВК33): the snapshot (404 is the handler's), whether
 * the console restores it, the agent, its revocation, its repository and plugin, the config.
 */
export function resolveRestore(
  found: NonNullable<ReturnType<typeof snapshotOf>>,
  request: Schemas['RestoreRequest'],
): Choice | Problem {
  const { source, snapshot } = found
  if (!snapshot.restorable) {
    return {
      status: 422,
      body: {
        ...problem(422, 'Unprocessable Content', 'snapshot_not_restorable'),
        status: 422,
        detail: null,
        errors: [{ field: 'snapshotId', message: 'cannot be restored from the console' }],
      },
    }
  }
  const held = holder(source, snapshot, request.agentId)
  if (!('agent' in held)) return held
  const { agent, repository } = held
  const config = request.config ?? {}
  const plugin = restorePlugins[source.plugin] as RestorePlugin
  const error = plugin.configError(config, agent)
  if (error !== null) return invalid(error.field, error.message)
  const target = plugin.target(agent, config)
  return { source, snapshot, agent, repositoryName: repository, config, target }
}

export function isChoice(value: Choice | Problem): value is Choice {
  return 'snapshot' in value
}

/** What the preview says: the place, and the warnings (an offline agent first, ВК36). */
export function previewOf(choice: Choice): Schemas['RestorePreview'] {
  const online = choice.agent.status === 'online'
  const plugin = restorePlugins[choice.source.plugin] as RestorePlugin
  return {
    sourceName: choice.source.name,
    snapshotId: choice.snapshot.snapshotId,
    createdAt: choice.snapshot.createdAt,
    totalBytes: choice.snapshot.totalBytes,
    agentId: choice.agent.id,
    hostname: choice.agent.hostname,
    agentOnline: online,
    target: choice.target,
    paths: plugin.paths(choice.config),
    restoreGlobals: plugin.restoreGlobals(choice.config),
    warnings: [...(online ? [] : (['agent_offline'] as const)), ...plugin.warnings(choice)],
  }
}

/** A restore run as the server queues it: one restore step on the chosen agent, no backup limit (ВК35). */
export function queuedRestore(choice: Choice): Schemas['Run'] {
  const now = new Date().toISOString()
  const { source, snapshot, agent } = choice
  const step: Schemas['RunStep'] = {
    id: crypto.randomUUID(),
    ordinal: 0,
    action: 'restore',
    status: 'queued',
    phase: null,
    agentId: agent.id,
    sourceId: source.id,
    plugin: source.plugin,
    repositoryName: choice.repositoryName,
    bytesProcessed: null,
    bytesTotal: null,
    filesProcessed: null,
    filesTotal: null,
    message: null,
    backup: null,
    restore: null,
    restoreOutcome: null,
    queuedAt: now,
    dispatchedAt: null,
    startedAt: null,
    finishedAt: null,
  }
  return {
    id: crypto.randomUUID(),
    sourceId: source.id,
    sourceName: source.name,
    sourceDeleted: !state.sources.some((s) => s.id === source.id),
    agentId: agent.id,
    trigger: 'manual',
    status: 'queued',
    message: null,
    queuedAt: now,
    startedAt: null,
    finishedAt: null,
    steps: [step],
    catchUp: null,
    kind: 'restore',
    requestedBy: 'admin',
    restore: {
      snapshotId: snapshot.snapshotId,
      snapshotCreatedAt: snapshot.createdAt,
      repositoryName: choice.repositoryName,
      config: choice.config,
      target: choice.target,
    },
  }
}

const filesRestore: RestorePlugin = {
  intoDirectory: true,
  schema: filesRestoreSchema as Schemas['RestoreTarget']['restoreConfigSchema'],
  defaults: (source, snapshot) => ({
    destination: defaultDestination(source.name, snapshot.createdAt),
  }),
  configError: (config) => configError(config),
  target: (agent, config) =>
    `${(agent.restoreDir ?? '').replace(/\/+$/, '')}/${String(config.destination)}`,
  paths: (config) => (Array.isArray(config.paths) ? (config.paths as string[]) : []),
  restoreGlobals: () => false,
  warnings: () => ['owners_not_restored'],
}

const DEFAULT_PORT = 5432
const MAX_NAME_BYTES = 63

// <database>_restored_<day of the snapshot, UTC>, the database cut at a character to fit 63 bytes.
export function newDatabaseName(database: string, createdAt: string): string {
  const suffix = `_restored_${createdAt.slice(0, 10).replace(/-/g, '')}`
  const room = MAX_NAME_BYTES - new TextEncoder().encode(suffix).length
  let used = 0
  let kept = ''
  for (const char of database) {
    used += new TextEncoder().encode(char).length
    if (used > room) break
    kept += char
  }
  return kept + suffix
}

const CONNECTION = ['host', 'port', 'user', 'password_ref', 'tls_mode', 'tls_root_cert']

interface Property {
  type?: string
  format?: string
  enum?: unknown[]
  minLength?: number
  maxLength?: number
  minimum?: number
  maximum?: number
  pattern?: string
}

// The first thing the schema refuses, as the server words its fields: config/<property>, or config
// for an unknown property; a secret must be one of the agent's.
function schemaError(
  schema: { properties: Record<string, Property>; required: string[] },
  config: Config,
  secrets: string[],
): Violation | null {
  const unknown = Object.keys(config).find((name) => !(name in schema.properties))
  if (unknown !== undefined) return { field: 'config', message: 'is not allowed' }
  const missing = schema.required.find((name) => config[name] === undefined)
  if (missing !== undefined) return { field: `config/${missing}`, message: 'is required' }
  for (const [name, value] of Object.entries(config)) {
    const message = propertyError(schema.properties[name], value, secrets)
    if (message !== null) return { field: `config/${name}`, message }
  }
  return null
}

const TYPES: Record<string, (value: unknown) => boolean> = {
  string: (value) => typeof value === 'string',
  integer: (value) => Number.isInteger(value),
  boolean: (value) => typeof value === 'boolean',
}

function propertyError(property: Property, value: unknown, secrets: string[]): string | null {
  if (property.type !== undefined && !TYPES[property.type]?.(value)) {
    return `must be of type ${property.type}`
  }
  if (typeof value === 'number') return rangeError(property, value)
  return typeof value === 'string' ? stringError(property, value, secrets) : null
}

function rangeError(property: Property, value: number): string | null {
  const below = property.minimum !== undefined && value < property.minimum
  const above = property.maximum !== undefined && value > property.maximum
  return below || above ? 'is out of range' : null
}

type StringRule = (property: Property, value: string, secrets: string[]) => string | null

const STRING_RULES: StringRule[] = [
  (p, v) => (p.enum !== undefined && !p.enum.includes(v) ? 'is not one of the values' : null),
  (p, v) => (p.minLength !== undefined && [...v].length < p.minLength ? 'is too short' : null),
  (p, v) => (p.maxLength !== undefined && [...v].length > p.maxLength ? 'is too long' : null),
  (p, v) =>
    p.pattern !== undefined && !new RegExp(p.pattern).test(v) ? 'does not fit the pattern' : null,
  (p, v, secrets) =>
    p.format === 'sard-secret' && !secrets.includes(v) ? 'is not a secret of the agent' : null,
]

// The first rule the string breaks.
function stringError(property: Property, value: string, secrets: string[]): string | null {
  for (const rule of STRING_RULES) {
    const message = rule(property, value, secrets)
    if (message !== null) return message
  }
  return null
}

const postgresqlRestore: RestorePlugin = {
  intoDirectory: false,
  schema: postgresqlRestoreSchema as unknown as Schemas['RestoreTarget']['restoreConfigSchema'],
  defaults: (source, snapshot) => {
    const backup = source.config as Config
    const connection = Object.fromEntries(
      CONNECTION.filter((name) => name in backup).map((name) => [name, backup[name]]),
    )
    const database = typeof backup.database === 'string' ? backup.database : source.name
    return {
      ...connection,
      database: newDatabaseName(database, snapshot.createdAt),
      restore_globals: false,
    }
  },
  configError: (config, agent) =>
    schemaError(
      postgresqlRestoreSchema as unknown as Parameters<typeof schemaError>[0],
      config,
      agent.secretNames,
    ),
  target: (_agent, config) => String(config.database),
  paths: () => [],
  restoreGlobals: (config) => config.restore_globals === true,
  warnings: (choice) => {
    const kind = postgresqlRestore.restoreGlobals(choice.config)
      ? ('globals_change_cluster' as const)
      : ('pg_owners_not_restored' as const)
    return sameCluster(choice) ? [kind, 'pg_same_cluster'] : [kind]
  },
}

// ВК32b: the agent of the backup and the host and port of the backup (the host without case).
function sameCluster({ agent, snapshot, source, config }: Choice): boolean {
  const backup = source.config as Config
  const host = typeof config.host === 'string' ? config.host : ''
  const port = (value: unknown) => (typeof value === 'number' ? value : DEFAULT_PORT)
  return (
    agent.id === snapshot.agentId &&
    host !== '' &&
    host.toLowerCase() === String(backup.host ?? '').toLowerCase() &&
    port(config.port) === port(backup.port)
  )
}

const restorePlugins: Record<string, RestorePlugin | undefined> = {
  files: filesRestore,
  postgresql: postgresqlRestore,
}
