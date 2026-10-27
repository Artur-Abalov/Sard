// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../../api/schema'
import {
  buildCommand,
  freeName,
  passwordRevealCommand,
  recoveryKeyCommand,
  safeLocation,
} from '../commands'
import { http } from '../http'
import { noSession, PROBLEM, problem } from '../problems'
import { status } from '../fixtures'
import { CONFIGS, NAMES, nightly, PLUGINS, ROLES, type Role } from '../selfBackupSources'
import { state } from '../state'
import { activeRun, queuedRun } from './sources'
import { FINGERPRINT } from './tokens'
import { neverVerified } from '../verification'
import { noTimeouts } from '../timeouts'

type Schemas = components['schemas']

// The mock of F6: the same refusals in the same order as the server, two system sources with a
// nightly schedule, "back up now" answering with the active run of a source that has one.

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

/** The source's newest finished run with [status], or its newest finished run at all. */
function lastFinished(sourceId: string, status?: Schemas['RunStatus']): Schemas['Run'] | undefined {
  return state.runs
    .filter((r) => r.sourceId === sourceId && r.finishedAt !== null)
    .filter((r) => status === undefined || r.status === status)
    .sort((a, b) => (b.finishedAt ?? '').localeCompare(a.finishedAt ?? ''))[0]
}

function systemSourceOf(source: Schemas['Source']): Schemas['SelfBackupSource'] {
  const last = lastFinished(source.id)
  return {
    role: source.systemRole as Role,
    sourceId: source.id,
    agentId: source.agentId,
    repositoryName: source.repositoryName,
    lastSuccessAt: lastFinished(source.id, 'succeeded')?.finishedAt ?? null,
    lastFailureAt: lastFinished(source.id, 'failed')?.finishedAt ?? null,
    lastRun:
      last === undefined
        ? null
        : { id: last.id, status: last.status, finishedAt: last.finishedAt as string },
  }
}

/** How long the neighbour takes to report a repository, and a run to end (Рк18). */
const SETTLE_MS = 3000
const PLAN_EXCLUDES = ['*.dump', '*.tgz', '*.tar', '*.tar.gz', '*.zst']

/** The server's rules, worst first; the mock knows no overdue episodes. */
function warningsOf(
  sources: Schemas['SelfBackupSource'][],
  agentId: string | null,
  local: boolean,
  keysUnconfirmed: boolean,
): Schemas['SelfBackupWarningCode'][] {
  const scheduleOff = (id: string) => !state.schedules.some((s) => s.sourceId === id && s.enabled)
  const checks: [Schemas['SelfBackupWarningCode'], boolean][] = [
    ['agent_replaced', sources.some((s) => s.agentId !== agentId)],
    ['failing', sources.some((s) => s.lastRun?.status === 'failed')],
    ['schedule_disabled', sources.some((s) => scheduleOff(s.sourceId))],
    ['local_storage', local],
    ['keys_unconfirmed', keysUnconfirmed],
  ]
  return checks.filter(([, holds]) => holds).map(([code]) => code)
}

function statusOf(warnings: Schemas['SelfBackupWarningCode'][]): Schemas['SelfBackupStatus'] {
  const worst = warnings[0]
  return worst === undefined || worst === 'local_storage' || worst === 'keys_unconfirmed'
    ? 'ok'
    : worst
}

/** The neighbour reports the repository the last command was built for, a while after it was asked for. */
function reportPendingRepository(): void {
  const pending = state.pendingRepository
  const agent = liveBuiltin()
  if (pending === null || agent === undefined || Date.now() - pending.askedAt < SETTLE_MS) return
  state.pendingRepository = null
  if (agent.repositories.some((r) => r.name === pending.name)) return
  const repositoryId = Array.from(crypto.getRandomValues(new Uint8Array(32)), (b) =>
    b.toString(16).padStart(2, '0'),
  ).join('')
  agent.repositories.push({
    name: pending.name,
    backend: pending.backend,
    repositoryId,
    cryptoProvider: 'file',
    recoveryKey: 'present',
  })
}

/** Runs that were due to end by now end: succeeded, or failed with a message of the step. */
export function settleRuns(): void {
  const now = Date.now()
  for (const [id, outcome] of Object.entries(state.runOutcomes)) {
    if (outcome.at > now) continue
    delete state.runOutcomes[id]
    const run = state.runs.find((r) => r.id === id)
    if (run === undefined) continue
    const finishedAt = new Date(outcome.at).toISOString()
    Object.assign(run, {
      status: outcome.status,
      outcome: outcome.status,
      message: outcome.message,
      finishedAt,
    })
    for (const step of run.steps) {
      Object.assign(step, { status: outcome.status, message: outcome.message, finishedAt })
    }
  }
}

function succeededSince(source: Schemas['Source'], since: string): boolean {
  return state.runs.some(
    (r) =>
      r.sourceId === source.id &&
      r.status === 'succeeded' &&
      Date.parse(r.queuedAt) >= Date.parse(since),
  )
}

/** Both system sources backed up successfully after the binding (Р20). */
function firstBackupDone(): boolean {
  const sources = systemSources()
  const bound = state.selfBackup
  if (bound === null || sources.length !== ROLES.length) return false
  return sources.every((source) => succeededSince(source, bound.boundAt))
}

/** The key copy of the current binding: confirmed at or after the binding. */
function keysConfirmedAt(): string | null {
  const bound = state.selfBackup
  const at = bound?.keysConfirmedAt ?? null
  return bound !== null && at !== null && Date.parse(at) >= Date.parse(bound.boundAt) ? at : null
}

/** The steps after the admin step, by the rules of the server (Р20). */
export function selfBackupSteps(adminDone: boolean): Schemas['OnboardingStep'][] {
  settleRuns()
  const complete = state.onboarding.keysDone
  const selfBackupDone = complete || (adminDone && firstBackupDone())
  const selfBackup = selfBackupDone ? 'done' : adminDone ? 'pending' : 'upcoming'
  const keys = complete ? 'done' : selfBackupDone ? 'pending' : 'upcoming'
  return [
    { id: 'self_backup', state: selfBackup },
    { id: 'keys_confirmed', state: keys },
  ]
}

function planOf(): Schemas['SelfBackupPlanItem'][] {
  return ROLES.map((role) => {
    const config = CONFIGS[role]
    const includes =
      role === 'self_database'
        ? [`${String(config.database)} на ${String(config.host)}:${String(config.port)}`]
        : (config.paths as string[])
    const excludes = role === 'self_database' ? [] : PLAN_EXCLUDES
    return { role, name: NAMES[role], plugin: PLUGINS[role], includes, excludes }
  })
}

function repositoriesOf(
  agent: Schemas['AgentDetails'] | undefined,
): Schemas['SelfBackupRepository'][] {
  return (agent?.repositories ?? [])
    .map((r) => ({
      name: r.name,
      backend: r.backend,
      repositoryId: r.repositoryId,
      local: r.backend === 'local',
    }))
    .sort((a, b) => a.name.localeCompare(b.name))
}

function current(): Schemas['SelfBackup'] {
  settleRuns()
  reportPendingRepository()
  const builtin = liveBuiltin()
  const agentId = builtin?.id ?? null
  const repositories = repositoriesOf(builtin)
  const sources = systemSources()
  if (state.selfBackup === null || sources.length !== ROLES.length) {
    return {
      configured: false,
      agentId,
      repository: null,
      boundAt: null,
      sources: [],
      status: 'not_configured',
      warnings: [],
      overdueSince: null,
      repositories,
      plan: planOf(),
      firstBackupDone: false,
      keysConfirmedAt: null,
      passwordRevealCommand: null,
      recoveryKeyCommand: null,
    }
  }
  const repository = repositoryOf(sources[0])
  const systemSourceViews = sources.map(systemSourceOf)
  const confirmedAt = keysConfirmedAt()
  const keysUnconfirmed = confirmedAt === null && state.onboarding.keysDone
  const warnings = warningsOf(systemSourceViews, agentId, repository.local, keysUnconfirmed)
  return {
    configured: true,
    agentId,
    repository,
    boundAt: state.selfBackup.boundAt,
    sources: systemSourceViews,
    status: statusOf(warnings),
    warnings,
    overdueSince: null,
    repositories,
    plan: planOf(),
    firstBackupDone: firstBackupDone(),
    keysConfirmedAt: confirmedAt,
    passwordRevealCommand: passwordRevealCommand(repository.name),
    recoveryKeyCommand: recoveryKeyCommand(repository.name),
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
      ...neverVerified,
      ...noTimeouts,
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

/** The first run of the database ever started fails once when the failure is switched on (Рк18). */
function outcomeOf(source: Schemas['Source']): {
  status: 'succeeded' | 'failed'
  message: string | null
} {
  if (state.failFirstBackup && source.systemRole === 'self_database') {
    state.failFirstBackup = false
    return {
      status: 'failed',
      message: 'restic: unable to open the repository: the storage refused the key',
    }
  }
  return { status: 'succeeded', message: null }
}

function startedRun(source: Schemas['Source']) {
  const run = queuedRun(source)
  state.runs.unshift(run)
  state.runOutcomes[run.id] = { at: Date.now() + SETTLE_MS, ...outcomeOf(source) }
  return run
}

const notConfigured = problem(409, 'Conflict', 'self_backup_not_configured')

/** The repository the binding names, or the address that does not fit, as the server refuses them. */
function locationRefusal(input: Schemas['SelfBackupRepositoryInput']) {
  const location = input.location
  if (location === undefined || location === null || safeLocation(location)) return null
  return refused('validation_failed', [
    { field: 'location', message: 'holds credentials or is not an address' },
  ])
}

/** Creates the system sources or moves them; the binding is marked bound again only when something moved. */
function bind(agent: Schemas['AgentDetails'], input: Schemas['SelfBackupRepositoryInput']): void {
  const now = new Date().toISOString()
  const moved = ROLES.map((role) => keep(role, agent.id, input.repositoryName, now)).some(Boolean)
  const location = input.location ?? null
  if (state.selfBackup === null || moved) {
    state.selfBackup = { boundAt: now, location, keysConfirmedAt: null }
  } else if (location !== null) {
    state.selfBackup.location = location
  }
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
    reportPendingRepository()
    const invalid = locationRefusal(input) ?? refusal(input, agent)
    if (invalid !== null) return response(422).json(invalid, PROBLEM)
    if (agent === undefined) return response(422).json(refused('self_agent_missing', []), PROBLEM)
    bind(agent, input)
    return response(200).json(current())
  }),

  http.post('/api/v1/self-backup/runs', ({ response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    settleRuns()
    if (!current().configured) return response(409).json(notConfigured, PROBLEM)
    const runs = systemSources().map((source) => {
      const role = source.systemRole as Role
      const active = activeRun(source.id)
      if (active !== undefined)
        return { role, sourceId: source.id, runId: active.id, started: false }
      return { role, sourceId: source.id, runId: startedRun(source).id, started: true }
    })
    return response(200).json({ runs })
  }),

  http.post('/api/v1/self-backup/keys-confirmation', async ({ request, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const body = await request.json()
    if ((body as { copiedOutsideServer?: unknown }).copiedOutsideServer !== true) {
      const invalid = refused('validation_failed', [
        { field: 'copiedOutsideServer', message: 'must be true' },
      ])
      return response(422).json(invalid, PROBLEM)
    }
    settleRuns()
    if (!current().configured || state.selfBackup === null) {
      return response(409).json(notConfigured, PROBLEM)
    }
    if (!firstBackupDone()) {
      return response(409).json(problem(409, 'Conflict', 'first_backup_pending'), PROBLEM)
    }
    if (keysConfirmedAt() === null) state.selfBackup.keysConfirmedAt = new Date().toISOString()
    state.onboarding.keysDone = true
    return response(204).empty()
  }),

  http.get('/api/v1/self-backup/repository-command', ({ query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    reportPendingRepository()
    const agent = liveBuiltin()
    const name = freeName((agent?.repositories ?? []).map((r) => r.name))
    const params: Record<string, string | undefined> = {}
    for (const name of COMMAND_PARAMETERS) params[name] = query.get(name) ?? undefined
    const built = buildCommand(name, params)
    if ('invalid' in built) {
      const errors = built.invalid.map((field) => ({
        field,
        message: 'is missing or not acceptable',
      }))
      return response(422).json(refused('validation_failed', errors), PROBLEM)
    }
    if (agent === undefined) return response(422).json(refused('self_agent_missing', []), PROBLEM)
    state.pendingRepository = {
      name,
      backend: built.backend,
      local: built.backend === 'local',
      askedAt: Date.now(),
    }
    return response(200).json({
      repositoryName: name,
      address: built.address,
      commands: built.commands,
    })
  }),

  http.get('/api/v1/self-backup/recovery-card', ({ response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const view = current()
    if (!view.configured || view.repository === null || state.selfBackup === null) {
      return response(409).json(notConfigured, PROBLEM)
    }
    return response(200).json({
      repositoryName: view.repository.name,
      backend: view.repository.backend,
      location: state.selfBackup.location,
      repositoryId: view.repository.repositoryId,
      caFingerprint: FINGERPRINT,
      sardVersion: status.version,
      generatedAt: new Date().toISOString(),
      recoveryProcedure: `https://github.com/Artur-Abalov/Sard/blob/main/${RECOVERY_DOC}`,
    })
  }),
]

const COMMAND_PARAMETERS = [
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
] as const

const RECOVERY_DOC = 'docs/operator/06-data-and-backup.md#восстановление-из-самобэкапа-f7'
