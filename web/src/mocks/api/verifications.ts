// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../../api/schema'
import { status as serverStatus } from '../fixtures'
import { http } from '../http'
import { pageOf } from '../paging'
import { noSession, notFound, PROBLEM, problem } from '../problems'
import { onboardingIncomplete, refusedUntilComplete } from '../restriction'
import { state } from '../state'
import { validateReportPeriod, validateStaleAfterDays } from '../validation'
import { withVerification } from '../verificationFixtures'
import { failureEpisodes } from '../verificationReport'

type Schemas = components['schemas']

const DAY_MS = 86_400_000
// A queued job of the mocks ends by itself after this long, as a verifier would end it: the tab stops polling.
const JOB_MS = 6_000
const FREE_BYTES = 128 * 1024 ** 3

function liveSource(sourceId: string): Schemas['Source'] | undefined {
  return state.sources.find((s) => s.id === sourceId)
}

const WANTED_IN_CONFIG: Record<string, { key: string; hint: Schemas['VerificationHintCode'] }> = {
  postgresql: { key: 'key_tables', hint: 'key_tables_missing' },
  files: { key: 'control_paths', hint: 'control_paths_missing' },
}

/** What the config of the source lacks for a better verification (rule 9): the same rule as the server's. */
function hintsOf(source: Schemas['Source']): Schemas['VerificationHintCode'][] {
  const wanted = WANTED_IN_CONFIG[source.plugin]
  if (wanted === undefined) return []
  const list: unknown = (source.config as Record<string, unknown>)[wanted.key]
  return Array.isArray(list) && list.length > 0 ? [] : [wanted.hint]
}

function stateOf(source: Schemas['Source']): Schemas['SourceVerificationState'] {
  return {
    enabled: source.verificationEnabled,
    status: source.verificationStatus,
    lastVerifiedAt: source.lastVerifiedAt,
    lastVerifiedLevel: source.lastVerifiedLevel,
    staleSince: null,
    openJob: state.verificationJobs[source.id] ?? null,
    hints: hintsOf(source),
  }
}

/** A job older than a few seconds is over: a pass of level 5 is recorded and the source's metric moves. */
function settleJob(sourceId: string): void {
  const job = state.verificationJobs[sourceId]
  if (job === undefined || Date.now() - Date.parse(job.createdAt) < JOB_MS) return
  const source = liveSource(sourceId)
  if (source === undefined) return
  const verifiedAt = new Date().toISOString()
  const template = state.verifications.find((r) => r.status === 'passed' && r.level === 5)
  const record: Schemas['Verification'] = {
    ...(template ?? state.verifications[0]),
    id: crypto.randomUUID(),
    jobId: job.id,
    sourceId,
    status: 'passed',
    level: 5,
    reason: null,
    mismatches: [],
    unsuitable: null,
    verifiedAt,
  }
  state.verifications = [record, ...state.verifications]
  delete state.verificationJobs[sourceId]
  const { verificationStatus, lastVerifiedAt, lastVerifiedLevel } = withVerification(
    source,
    state.verifications,
  )
  Object.assign(source, { verificationStatus, lastVerifiedAt, lastVerifiedLevel })
}

/** Why a verifier does not take a job of a source whose repository has the id [repository]: by the mock's own facts. */
function reasonsOf(
  agent: Schemas['AgentDetails'],
  repository: string | null,
): Schemas['VerifierReason'][] {
  const online = agent.status === 'online'
  const holdsKey = agent.repositories.some(
    (r) => r.repositoryId === repository && repository !== null,
  )
  const verifies = agent.plugins.some((p) => p.actions.includes('verify'))
  return [
    ...(online ? [] : (['offline'] as const)),
    ...(holdsKey ? [] : (['no_key'] as const)),
    ...(verifies ? [] : (['no_plugin'] as const)),
    ...(online ? [] : (['space_unknown'] as const)),
  ]
}

function verifierOf(
  agent: Schemas['AgentDetails'],
  reasons: Schemas['VerifierReason'][] | null,
): Schemas['Verifier'] {
  const month = state.verifications.filter(
    (r) => r.verifierAgentId === agent.id && Date.parse(r.verifiedAt) >= Date.now() - 30 * DAY_MS,
  )
  const online = agent.status === 'online'
  const verifyPlugins = agent.plugins.filter((p) => p.actions.includes('verify'))
  return {
    agentId: agent.id,
    hostname: agent.hostname,
    online,
    reported: true,
    freeBytes: online ? FREE_BYTES : null,
    sandbox: online ? 'podman' : null,
    sandboxVersion: online ? '5.2.2' : null,
    repositoryIds: agent.repositories.flatMap((r) =>
      r.repositoryId === null ? [] : [r.repositoryId],
    ),
    verifyPlugins: verifyPlugins.map((p) => p.name),
    busy: false,
    recentVerifications: month.filter((r) => Date.parse(r.verifiedAt) >= Date.now() - DAY_MS)
      .length,
    verificationsDone: month.length,
    infrastructureFailures: month.filter((r) => r.status === 'infrastructure_failure').length,
    reasons,
  }
}

/** The verifiers are the agents of the role that are not revoked. */
function verifiers(sourceId: string | null): Schemas['Verifier'][] {
  const source = sourceId === null ? undefined : liveSource(sourceId)
  const home = state.agents.find((a) => a.id === source?.agentId)
  const repository =
    home?.repositories.find((r) => r.name === source?.repositoryName)?.repositoryId ?? null
  return state.agents
    .filter((a) => a.role === 'verifier' && a.revokedAt === null)
    .map((agent) => verifierOf(agent, sourceId === null ? null : reasonsOf(agent, repository)))
}

/** The first verifier's setup: one key command per agent and repository of the sources whose verification is on. */
function setup(): Schemas['VerificationSetup'] {
  const pairs = new Map<string, Schemas['SetupRepository']>()
  for (const source of state.sources.filter((s) => s.verificationEnabled)) {
    const agent = state.agents.find((a) => a.id === source.agentId)
    if (agent === undefined) continue
    pairs.set(`${agent.id} ${source.repositoryName}`, {
      sourceAgentId: agent.id,
      sourceHostname: agent.hostname,
      repositoryName: source.repositoryName,
      keyAddCommand: `sudo sard-agent repo key add ${source.repositoryName} --for verifier`,
    })
  }
  const repositories = [...pairs.values()].sort(
    (a, b) =>
      a.sourceHostname.localeCompare(b.sourceHostname) ||
      a.repositoryName.localeCompare(b.repositoryName),
  )
  return {
    tokenRole: 'verifier',
    repositories,
    verifierRepoAdd: 'sudo sard-agent repo add <name> <address> --password-from-file <file>',
    readOnlyNote: "Give the verifier's host a storage account that can only read the repository.",
  }
}

function report(
  source: Schemas['Source'],
  from: string | null,
  to: string | null,
): Schemas['VerificationReportResponse'] {
  const now = Date.now()
  const end = to === null ? now : Date.parse(to) + DAY_MS
  const start = from === null ? end - 30 * DAY_MS : Date.parse(from)
  const own = state.verifications.filter((r) => r.sourceId === source.id)
  const items = own
    .filter((r) => Date.parse(r.verifiedAt) >= start && Date.parse(r.verifiedAt) < end)
    .map((r) => ({
      id: r.id,
      verifiedAt: r.verifiedAt,
      resticSnapshotId: r.resticSnapshotId,
      verifierHostname: state.agents.find((a) => a.id === r.verifierAgentId)?.hostname ?? null,
      status: r.status,
      level: r.level,
      durationMs: r.stages.length === 0 ? null : r.stages.length * 15_000,
      restoreDurationMs: r.restoreDurationMs,
    }))
  const count = (status: Schemas['VerificationOutcome']) =>
    items.filter((i) => i.status === status).length
  return {
    sourceId: source.id,
    sourceName: source.name,
    sourceDeleted: false,
    from: new Date(start).toISOString(),
    to: new Date(end).toISOString(),
    timezone: state.verificationSchedule.timezone,
    generatedAt: new Date(now).toISOString(),
    serverVersion: serverStatus.version,
    summary: {
      total: items.length,
      passed: count('passed'),
      failed: count('failed'),
      notHeld: items.length - count('passed') - count('failed'),
    },
    items,
    failures: failureEpisodes(own, start, end),
  }
}

// The queue, the verifiers and the history live on the server (V4); the mocks keep what the console reads.
export const verificationHandlers = [
  http.get('/api/v1/sources/{sourceId}/verification', ({ params, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const source = liveSource(params.sourceId)
    if (source === undefined) return response(404).json(notFound, PROBLEM)
    settleJob(source.id)
    return response(200).json(stateOf(source))
  }),

  http.put('/api/v1/sources/{sourceId}/verification', async ({ params, request, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    if (refusedUntilComplete()) return response(409).json(onboardingIncomplete, PROBLEM)
    const source = liveSource(params.sourceId)
    if (source === undefined) return response(404).json(notFound, PROBLEM)
    const input = await request.json()
    source.verificationEnabled = input.enabled
    return response(200).json(stateOf(source))
  }),

  http.get('/api/v1/sources/{sourceId}/verifications', ({ params, query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    if (liveSource(params.sourceId) === undefined) return response(404).json(notFound, PROBLEM)
    settleJob(params.sourceId)
    const items = state.verifications.filter((v) => v.sourceId === params.sourceId)
    return response(200).json(pageOf(items, query.get('cursor'), Number(query.get('limit') ?? 50)))
  }),

  http.post('/api/v1/sources/{sourceId}/verifications', ({ params, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    if (refusedUntilComplete()) return response(409).json(onboardingIncomplete, PROBLEM)
    if (liveSource(params.sourceId) === undefined) return response(404).json(notFound, PROBLEM)
    if (state.verificationJobs[params.sourceId] !== undefined) {
      return response(409).json(problem(409, 'Conflict', 'verification_active'), PROBLEM)
    }
    const now = Date.now()
    const job: Schemas['VerificationJob'] = {
      id: crypto.randomUUID(),
      status: 'queued',
      reason: 'manual',
      createdAt: new Date(now).toISOString(),
      deadline: new Date(now + DAY_MS).toISOString(),
      failedAttempts: 0,
      unsuitable: null,
    }
    state.verificationJobs[params.sourceId] = job
    return response(202).json(job)
  }),

  http.get('/api/v1/sources/{sourceId}/verification-report', ({ params, query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const source = liveSource(params.sourceId)
    if (source === undefined) return response(404).json(notFound, PROBLEM)
    const from = query.get('from')
    const to = query.get('to')
    const refused = validateReportPeriod(from, to)
    if (refused !== null) return response(422).json(refused, PROBLEM)
    return response(200).json(report(source, from, to))
  }),

  http.get('/api/v1/verifications/{verificationId}', ({ params, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const verification = state.verifications.find((v) => v.id === params.verificationId)
    if (verification === undefined) return response(404).json(notFound, PROBLEM)
    return response(200).json(verification)
  }),

  http.get('/api/v1/verification-schedule', ({ response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    return response(200).json(state.verificationSchedule)
  }),

  http.put('/api/v1/verification-schedule', async ({ request, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    if (refusedUntilComplete()) return response(409).json(onboardingIncomplete, PROBLEM)
    const input = await request.json()
    if (input.staleAfterDays !== undefined && input.staleAfterDays !== null) {
      const refused = validateStaleAfterDays(Number(input.staleAfterDays))
      if (refused !== null) return response(422).json(refused, PROBLEM)
    }
    // The mock does not evaluate cron: the next moment is «in a day».
    const nextRunAt = new Date(Date.now() + DAY_MS).toISOString()
    state.verificationSchedule = {
      cron: input.cron,
      timezone: input.timezone,
      nextRunAt,
      staleAfterDays: Number(input.staleAfterDays ?? state.verificationSchedule.staleAfterDays),
    }
    return response(200).json(state.verificationSchedule)
  }),

  http.get('/api/v1/verification-setup', ({ response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    return response(200).json(setup())
  }),

  http.get('/api/v1/verifiers', ({ query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const sourceId = query.get('sourceId')
    if (sourceId !== null && liveSource(sourceId) === undefined)
      return response(404).json(notFound, PROBLEM)
    return response(200).json(verifiers(sourceId))
  }),
]
