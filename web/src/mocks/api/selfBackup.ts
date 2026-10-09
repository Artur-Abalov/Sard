// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../../api/schema'
import { http } from '../http'
import { noSession, PROBLEM, problem } from '../problems'
import { state } from '../state'
import { activeRun, queuedRun } from './sources'

type Schemas = components['schemas']
type Role = Schemas['SystemSourceRole']

// The mock of F6: the same refusals in the same order as the server, two system sources with a
// nightly schedule, "back up now" answering with the active run of a source that has one.

const ROLES: Role[] = ['self_database', 'self_keys']
const NAMES: Record<Role, string> = {
  self_database: 'Sard: database',
  self_keys: 'Sard: keys and configuration',
}
const PLUGINS: Record<Role, string> = { self_database: 'postgresql', self_keys: 'files' }
const CONFIGS: Record<Role, Record<string, unknown>> = {
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

function refused(
  code: Schemas['ErrorCode'],
  errors: Schemas['FieldError'][],
): Schemas['ValidationProblem'] {
  return { ...problem(422, 'Unprocessable Content', code), errors }
}

function liveBuiltin(): Schemas['AgentDetails'] | undefined {
  return state.agents.find((a) => a.builtin && a.revokedAt === null)
}

function systemSources(): Schemas['Source'][] {
  return ROLES.flatMap((role) => state.sources.filter((s) => s.systemRole === role))
}

function repositoryOf(source: Schemas['Source']): Schemas['SelfBackupRepository'] {
  const owner = state.agents.find((a) => a.id === source.agentId)
  const announced = owner?.repositories.find((r) => r.name === source.repositoryName)
  return {
    name: source.repositoryName,
    backend: announced?.backend ?? '',
    repositoryId: announced?.repositoryId ?? null,
    local: announced?.backend === 'local',
  }
}

function current(): Schemas['SelfBackup'] {
  const agentId = liveBuiltin()?.id ?? null
  const sources = systemSources()
  if (state.selfBackup === null || sources.length !== ROLES.length) {
    return { configured: false, agentId, repository: null, boundAt: null, sources: [] }
  }
  return {
    configured: true,
    agentId,
    repository: repositoryOf(sources[0]),
    boundAt: state.selfBackup.boundAt,
    sources: sources.map((s) => ({
      role: s.systemRole as Role,
      sourceId: s.id,
      agentId: s.agentId,
      repositoryName: s.repositoryName,
    })),
  }
}

/** What the server refuses before it touches a source; null when the binding may go ahead. */
function refusal(
  input: Schemas['SelfBackupRepositoryInput'],
  agent: Schemas['AgentDetails'] | undefined,
): Schemas['ValidationProblem'] | null {
  if (agent === undefined) return refused('self_agent_missing', [])
  const repository = agent.repositories.find((r) => r.name === input.repositoryName)
  if (repository === undefined) {
    return refused('unknown_repository', [
      { field: 'repositoryName', message: "is not the agent's repository" },
    ])
  }
  if (repository.repositoryId === null) {
    return refused('repository_not_initialized', [
      { field: 'repositoryName', message: 'has no restic repository id yet' },
    ])
  }
  if (repository.backend === 'local' && input.confirmLocalStorage !== true) {
    return refused('local_storage_unconfirmed', [
      {
        field: 'confirmLocalStorage',
        message: "must be true for a repository on the server's own machine",
      },
    ])
  }
  if (!agent.secretNames.includes('sard-db')) {
    return refused('invalid_config', [{ field: 'config/password_ref', message: 'unknown secret' }])
  }
  return null
}

function nightly(sourceId: string, now: string): Schemas['Schedule'] {
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
    createdAt: now,
    updatedAt: now,
  }
}

/** Creates the source of [role] or moves it to [agentId] and [repositoryName]; true if anything changed. */
function keep(role: Role, agentId: string, repositoryName: string, now: string): boolean {
  const existing = state.sources.find((s) => s.systemRole === role)
  if (existing === undefined) {
    const source: Schemas['Source'] = {
      id: crypto.randomUUID(),
      name: NAMES[role],
      agentId,
      plugin: PLUGINS[role],
      repositoryName,
      config: CONFIGS[role],
      createdAt: now,
      updatedAt: now,
      systemRole: role,
    }
    state.sources.unshift(source)
    state.schedules.push(nightly(source.id, now))
    return true
  }
  if (existing.agentId === agentId && existing.repositoryName === repositoryName) return false
  Object.assign(existing, { agentId, repositoryName, updatedAt: now })
  return true
}

export const selfBackupHandlers = [
  http.get('/api/v1/self-backup', ({ response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    return response(200).json(current())
  }),

  http.put('/api/v1/self-backup/repository', async ({ request, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const input = await request.json()
    const agent = liveBuiltin()
    const invalid = refusal(input, agent)
    if (invalid !== null) return response(422).json(invalid, PROBLEM)
    if (agent === undefined) return response(422).json(refused('self_agent_missing', []), PROBLEM)
    const now = new Date().toISOString()
    const moved = ROLES.map((role) => keep(role, agent.id, input.repositoryName, now)).some(Boolean)
    if (state.selfBackup === null || moved) state.selfBackup = { boundAt: now }
    return response(200).json(current())
  }),

  http.post('/api/v1/self-backup/runs', ({ response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const view = current()
    if (!view.configured) {
      return response(409).json(problem(409, 'Conflict', 'self_backup_not_configured'), PROBLEM)
    }
    const runs = systemSources().map((source) => {
      const active = activeRun(source.id)
      if (active !== undefined) {
        return {
          role: source.systemRole as Role,
          sourceId: source.id,
          runId: active.id,
          started: false,
        }
      }
      const run = queuedRun(source)
      state.runs.unshift(run)
      return { role: source.systemRole as Role, sourceId: source.id, runId: run.id, started: true }
    })
    return response(200).json({ runs })
  }),
]
