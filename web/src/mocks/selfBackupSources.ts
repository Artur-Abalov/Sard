// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../api/schema'
import { neverVerified } from './verification'

type Schemas = components['schemas']
export type Role = Schemas['SystemSourceRole']

// What the mock of F6 knows of the two system sources, apart from the state: the handlers create them
// when the wizard binds a repository, and the default state holds them already bound (Рк18).

export const ROLES: Role[] = ['self_database', 'self_keys']
export const NAMES: Record<Role, string> = {
  self_database: 'Sard: database',
  self_keys: 'Sard: keys and configuration',
}
export const PLUGINS: Record<Role, string> = { self_database: 'postgresql', self_keys: 'files' }
export const CONFIGS: Record<Role, Record<string, unknown>> = {
  self_database: {
    host: 'postgres',
    port: 5432,
    database: 'sard',
    user: 'sard_self',
    password_ref: 'sard-db',
    tls_mode: 'disable',
    pg_dump_path: '/usr/lib/postgresql/18/bin/pg_dump',
    include_globals: false,
  },
  self_keys: { paths: ['/var/lib/sard/pki', '/etc/sard/install'], exclude: ['*.dump', '*.tgz'] },
}

export function nightly(sourceId: string, now: string): Schemas['Schedule'] {
  return {
    id: crypto.randomUUID(),
    sourceId,
    cron: '0 3 * * *',
    timezone: 'UTC',
    enabled: true,
    nextRunAt: new Date(Date.now() + 3_600_000).toISOString(),
    catchUpAt: null,
    lastFiredAt: null,
    skippedInRow: 0,
    notifyOnSuccess: false,
    lastRun: null,
    createdAt: now,
    updatedAt: now,
  }
}

/** The repository the default mock is bound to, and when (Рк18). */
export const DEFAULT_REPOSITORY = 'self'
export const DEFAULT_BOUND_AT = '2026-09-19T09:41:00Z'
export const DEFAULT_LOCATION = 's3:acme-sard/prod'
const DEFAULT_BACKED_UP_AT = '2026-09-19T09:42:00Z'
const DEFAULT_KEYS_CONFIRMED_AT = '2026-09-19T09:43:00Z'

function systemSource(role: Role, agentId: string): Schemas['Source'] {
  return {
    id: crypto.randomUUID(),
    name: NAMES[role],
    agentId,
    plugin: PLUGINS[role],
    ...neverVerified,
    repositoryName: DEFAULT_REPOSITORY,
    config: CONFIGS[role],
    createdAt: DEFAULT_BOUND_AT,
    updatedAt: DEFAULT_BOUND_AT,
    systemRole: role,
  }
}

function succeededRun(source: Schemas['Source']): Schemas['Run'] {
  const queuedAt = DEFAULT_BOUND_AT
  const step: Schemas['RunStep'] = {
    id: crypto.randomUUID(),
    ordinal: 0,
    action: 'backup',
    status: 'succeeded',
    phase: null,
    agentId: source.agentId,
    sourceId: source.id,
    plugin: source.plugin,
    repositoryName: source.repositoryName,
    bytesProcessed: null,
    bytesTotal: null,
    filesProcessed: null,
    filesTotal: null,
    message: null,
    backup: null,
    restore: null,
    restoreOutcome: null,
    queuedAt,
    dispatchedAt: queuedAt,
    startedAt: queuedAt,
    finishedAt: DEFAULT_BACKED_UP_AT,
  }
  return {
    id: crypto.randomUUID(),
    sourceId: source.id,
    sourceName: source.name,
    sourceDeleted: false,
    agentId: source.agentId,
    trigger: 'manual',
    status: 'succeeded',
    message: null,
    queuedAt,
    startedAt: queuedAt,
    finishedAt: DEFAULT_BACKED_UP_AT,
    steps: [step],
    catchUp: null,
    kind: 'backup',
    requestedBy: 'admin',
    restore: null,
  }
}

/** Nothing is bound: a clean or an upgraded installation (F4b). */
const UNBOUND = { sources: [], schedules: [], runs: [], binding: null }

/** The self-backup bound to [DEFAULT_REPOSITORY] of [agentId], both system sources backed up, the key copy confirmed. */
export function defaultSelfBackup(agentId: string | undefined) {
  if (agentId === undefined) return UNBOUND
  const sources = ROLES.map((role) => systemSource(role, agentId))
  return {
    sources,
    schedules: sources.map((source) => nightly(source.id, DEFAULT_BOUND_AT)),
    runs: sources.map(succeededRun),
    binding: {
      boundAt: DEFAULT_BOUND_AT,
      location: DEFAULT_LOCATION,
      keysConfirmedAt: DEFAULT_KEYS_CONFIRMED_AT,
    },
  }
}
