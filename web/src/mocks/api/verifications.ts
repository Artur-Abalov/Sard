// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../../api/schema'
import { http } from '../http'
import { pageOf } from '../paging'
import { noSession, notFound, PROBLEM, problem } from '../problems'
import { onboardingIncomplete, refusedUntilComplete } from '../restriction'
import { state } from '../state'
import { verifierId } from '../verification'

type Schemas = components['schemas']

const DAY_MS = 86_400_000

function liveSource(sourceId: string): Schemas['Source'] | undefined {
  return state.sources.find((s) => s.id === sourceId)
}

function stateOf(source: Schemas['Source']): Schemas['SourceVerificationState'] {
  return {
    enabled: source.verificationEnabled,
    status: source.verificationStatus,
    lastVerifiedAt: source.lastVerifiedAt,
    lastVerifiedLevel: source.lastVerifiedLevel,
    staleSince: null,
    openJob: state.verificationJobs[source.id] ?? null,
  }
}

/** The one verifier of the mocks: it holds the key, but no plugin of it verifies yet. */
function verifier(asked: boolean): Schemas['Verifier'] {
  return {
    agentId: verifierId,
    hostname: 'verify6',
    online: true,
    reported: true,
    freeBytes: null,
    sandbox: null,
    repositoryIds: [],
    verifyPlugins: [],
    busy: false,
    recentVerifications: 0,
    reasons: asked ? ['no_key', 'no_plugin', 'space_unknown'] : null,
  }
}

// The queue, the verifiers and the history live on the server (V4); the mocks keep only what the console reads.
export const verificationHandlers = [
  http.get('/api/v1/sources/{sourceId}/verification', ({ params, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const source = liveSource(params.sourceId)
    if (source === undefined) return response(404).json(notFound, PROBLEM)
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
    // The mock does not evaluate cron: the next moment is «in a day».
    const nextRunAt = new Date(Date.now() + DAY_MS).toISOString()
    state.verificationSchedule = { cron: input.cron, timezone: input.timezone, nextRunAt }
    return response(200).json(state.verificationSchedule)
  }),

  http.get('/api/v1/verifiers', ({ query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const sourceId = query.get('sourceId')
    if (sourceId !== null && liveSource(sourceId) === undefined)
      return response(404).json(notFound, PROBLEM)
    return response(200).json([verifier(sourceId !== null)])
  }),
]
