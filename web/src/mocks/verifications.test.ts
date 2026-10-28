// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import createClient from 'openapi-fetch'
import { describe, expect, test } from 'vitest'
import type { paths } from '../api/schema'
import { ids, MOCK_PASSWORD } from './fixtures'
import { state } from './state'

// The verification of the mocks as the console sees it (V6): the typed client against the MSW handlers.
const api = createClient<paths>({ baseUrl: location.origin })

function must<T>(result: { data?: T; response: Response }): T {
  if (result.data === undefined) throw new Error(`${result.response.status} ${result.response.url}`)
  return result.data
}

async function signIn() {
  await api.POST('/api/v1/session', { body: { password: MOCK_PASSWORD } })
}

describe('the fixtures of verification', () => {
  test('the records hold every outcome, both levels of a pass and mismatches', async () => {
    await signIn()
    const records = state.verifications

    const statuses = new Set(records.map((r) => r.status))
    expect([...statuses].sort()).toEqual(
      [
        'cancelled',
        'cannot_verify',
        'failed',
        'infrastructure_failure',
        'no_verifier',
        'passed',
      ].sort(),
    )
    const passedLevels = new Set(records.filter((r) => r.status === 'passed').map((r) => r.level))
    expect(passedLevels.has(4) && passedLevels.has(5)).toBe(true)
    expect(records.some((r) => r.status === 'failed' && r.mismatches.length > 0)).toBe(true)
    expect(records.some((r) => r.status === 'no_verifier' && (r.unsuitable?.length ?? 0) > 0)).toBe(
      true,
    )
  })

  test('there is a source nothing has verified and a source whose verification is off', () => {
    const verified = new Set(state.verifications.map((r) => r.sourceId))
    const live = state.sources
    expect(live.some((s) => s.verificationEnabled && !verified.has(s.id))).toBe(true)
    expect(live.some((s) => !s.verificationEnabled)).toBe(true)
  })

  test('the metric of every source agrees with its newest records', () => {
    for (const source of state.sources) {
      const own = state.verifications.filter((r) => r.sourceId === source.id)
      const last = own.find((r) => r.status !== 'cancelled')
      expect(source.verificationStatus, source.name).toBe(last?.status ?? null)
      const lastPass = own.find((r) => r.status === 'passed')
      expect(source.lastVerifiedAt, source.name).toBe(lastPass?.verifiedAt ?? null)
      expect(source.lastVerifiedLevel, source.name).toBe(lastPass?.level ?? null)
    }
  })

  test('a record has its fingerprint side by side when the snapshot kept one', async () => {
    await signIn()
    const failed = state.verifications.find(
      (r) => r.status === 'failed' && r.fingerprint.length > 0,
    )
    const record = must(
      await api.GET('/api/v1/verifications/{verificationId}', {
        params: { path: { verificationId: failed?.id ?? '' } },
      }),
    )
    expect(new Set(record.fingerprint.map((row) => row.outcome))).toEqual(
      new Set(['matched', 'mismatched', 'not_compared']),
    )
  })
})

describe('the overview of the mocks', () => {
  test('counts the sources by the rules of the server', async () => {
    await signIn()
    const verification = must(await api.GET('/api/v1/overview')).verification
    const enabled = state.sources.filter((s) => s.verificationEnabled)

    expect(verification.enabled).toBe(enabled.length)
    expect(verification.disabled).toBe(state.sources.length - enabled.length)
    expect(verification.failed).toBe(
      enabled.filter((s) => s.verificationStatus === 'failed').length,
    )
    expect(verification.notHeld).toBe(
      enabled.filter((s) =>
        ['cannot_verify', 'infrastructure_failure', 'no_verifier'].includes(
          s.verificationStatus ?? '',
        ),
      ).length,
    )
    expect(verification.neverVerified).toBe(
      enabled.filter((s) => s.verificationStatus === null).length,
    )
    expect(verification.noVerifier).toBe(false)
  })

  test('puts the worst sources first, at most ten', async () => {
    await signIn()
    const { attention } = must(await api.GET('/api/v1/overview')).verification

    expect(attention.length).toBeLessThanOrEqual(10)
    expect(attention[0].status).toBe('failed')
    expect(attention.every((item) => state.sources.some((s) => s.id === item.sourceId))).toBe(true)
  })

  test('says there is no verifier when the only one is revoked', async () => {
    await signIn()
    await api.POST('/api/v1/agents/{agentId}/revoke', {
      params: { path: { agentId: ids.verifierAgent } },
    })

    expect(must(await api.GET('/api/v1/overview')).verification.noVerifier).toBe(true)
  })
})

describe('the cancel of a verification job', () => {
  const path = { params: { path: { sourceId: ids.etcSource } } }

  async function openJob() {
    await signIn()
    const queued = await api.POST('/api/v1/sources/{sourceId}/verifications', { ...path, body: {} })
    return must(queued)
  }

  function cancel(jobId: string) {
    return api.POST('/api/v1/verification-jobs/{jobId}/cancel', { params: { path: { jobId } } })
  }

  test('a job nobody has is closed at once with a record cancelled by admin and no stages', async () => {
    const job = await openJob()

    const answer = must(await cancel(job.id))

    expect(answer.openJob).toBeNull()
    const record = state.verifications.find((r) => r.jobId === job.id)
    expect(record).toMatchObject({ status: 'cancelled', cancelledBy: 'admin', stages: [] })
    expect(record?.cancelledAt).not.toBeNull()
  })

  test('a job a verifier has is marked as being cancelled and closes when the verifier stops', async () => {
    const job = await openJob()
    state.verificationJobs[ids.etcSource].createdAt = new Date(Date.now() - 4_000).toISOString()

    const answer = must(await cancel(job.id))

    expect(answer.openJob?.status).toBe('running')
    expect(answer.openJob?.cancellation).toMatchObject({ requestedBy: 'admin', result: 'pending' })
    expect(state.verifications.some((r) => r.jobId === job.id)).toBe(false)
    const cancellation = state.verificationJobs[ids.etcSource].cancellation
    if (cancellation === null) throw new Error('the job was not marked')
    cancellation.requestedAt = new Date(Date.now() - 4_000).toISOString()

    const settled = must(await api.GET('/api/v1/sources/{sourceId}/verification', path))

    expect(settled.openJob).toBeNull()
    expect(state.verifications.find((r) => r.jobId === job.id)?.status).toBe('cancelled')
  })

  test('a cancelled record does not move the status of the source', async () => {
    const job = await openJob()
    const before = must(await api.GET('/api/v1/sources/{sourceId}/verification', path)).status

    await cancel(job.id)

    expect(must(await api.GET('/api/v1/sources/{sourceId}/verification', path)).status).toBe(before)
  })

  test('a job that is closed answers 409 run_finished, an unknown one 404', async () => {
    const job = await openJob()
    await cancel(job.id)

    const closed = await cancel(job.id)
    const unknown = await cancel('0192f7a0-0000-7000-8000-00000000dead')

    expect(closed.response.status).toBe(409)
    expect(closed.error?.code).toBe('run_finished')
    expect(unknown.response.status).toBe(404)
  })
})

describe('the endpoints of verification', () => {
  test('verify now answers 409 verification_active while a job is open', async () => {
    await signIn()
    const params = { params: { path: { sourceId: ids.etcSource } }, body: {} }

    const first = await api.POST('/api/v1/sources/{sourceId}/verifications', params)
    const second = await api.POST('/api/v1/sources/{sourceId}/verifications', params)

    expect(first.response.status).toBe(202)
    expect(second.response.status).toBe(409)
    expect(second.error?.code).toBe('verification_active')
  })

  test('the threshold out of range is 422 at staleAfterDays, a good one is kept', async () => {
    await signIn()
    const cycle = { cron: '30 4 * * *', timezone: 'UTC' }

    const refused = await api.PUT('/api/v1/verification-schedule', {
      body: { ...cycle, staleAfterDays: 0 },
    })
    expect(refused.response.status).toBe(422)
    expect(refused.error).toMatchObject({ errors: [{ field: 'staleAfterDays' }] })

    const saved = await api.PUT('/api/v1/verification-schedule', {
      body: { ...cycle, staleAfterDays: 3 },
    })
    expect(must(saved).staleAfterDays).toBe(3)
    const kept = await api.PUT('/api/v1/verification-schedule', { body: cycle })
    expect(must(kept).staleAfterDays).toBe(3)
  })

  test('the report with the end before the start is 422 at to', async () => {
    await signIn()

    const refused = await api.GET('/api/v1/sources/{sourceId}/verification-report', {
      params: {
        path: { sourceId: ids.etcSource },
        query: { from: '2026-10-26', to: '2026-10-01' },
      },
    })

    expect(refused.response.status).toBe(422)
    expect(refused.error).toMatchObject({ errors: [{ field: 'to' }] })
  })

  test('the report lists the records of the period, newest first, with a summary', async () => {
    await signIn()

    const report = must(
      await api.GET('/api/v1/sources/{sourceId}/verification-report', {
        params: { path: { sourceId: ids.etcSource } },
      }),
    )

    expect(report.sourceName).toBe('db1 /etc')
    expect(report.summary.total).toBe(report.items.length)
    expect(report.items.length).toBeGreaterThan(0)
  })

  test('the setup lists a key command for each repository of the sources', async () => {
    await signIn()

    const setup = must(await api.GET('/api/v1/verification-setup'))

    expect(setup.tokenRole).toBe('verifier')
    expect(setup.repositories.length).toBeGreaterThan(0)
    expect(setup.verifierRepoAdd).toContain('<address>')
  })

  test('the verifiers carry the version of the sandbox and their counters', async () => {
    await signIn()

    const [verifier] = must(await api.GET('/api/v1/verifiers'))

    expect(verifier.sandboxVersion).not.toBeUndefined()
    expect(verifier.verificationsDone).toBeGreaterThanOrEqual(0)
  })

  test('a source has hints when its config lacks key tables or control paths', async () => {
    await signIn()

    const files = must(
      await api.GET('/api/v1/sources/{sourceId}/verification', {
        params: { path: { sourceId: ids.etcSource } },
      }),
    )
    const database = must(
      await api.GET('/api/v1/sources/{sourceId}/verification', {
        params: { path: { sourceId: ids.appSource } },
      }),
    )

    expect(files.hints).toEqual(['control_paths_missing'])
    expect(database.hints).toEqual(['key_tables_missing'])
  })

  test('a finished verify now keeps the verification switch the user set', async () => {
    await signIn()
    const path = { params: { path: { sourceId: ids.etcSource } } }
    await api.PUT('/api/v1/sources/{sourceId}/verification', { ...path, body: { enabled: false } })
    await api.POST('/api/v1/sources/{sourceId}/verifications', { ...path, body: {} })
    const job = state.verificationJobs[ids.etcSource]
    job.createdAt = new Date(Date.now() - 60_000).toISOString()

    const settled = must(await api.GET('/api/v1/sources/{sourceId}/verification', path))

    expect(settled.enabled).toBe(false)
    expect(settled.status).toBe('passed')
  })
})
