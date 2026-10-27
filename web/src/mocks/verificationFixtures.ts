// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../api/schema'
import { ids } from './fixtures'

type Schemas = components['schemas']

const HOUR = 3_600_000

// Sources whose verification the fixtures turn off: a decision, neither a failure nor a silence (ОВ3).
const verificationOff = new Set([ids.appSource])

const stageNames = [
  ['snapshot', 0],
  ['fingerprint_tag', 0],
  ['work_volume', 0],
  ['restic_check', 1],
  ['sandbox', 0],
  ['restore', 2],
  ['start', 3],
  ['fingerprint', 4],
  ['cleanup', 0],
] as const

function stage(
  name: (typeof stageNames)[number],
  state: Schemas['VerificationStageStatus'],
  begin: number,
  output = '',
): Schemas['VerificationStage'] {
  const startedAt = new Date(begin).toISOString()
  return {
    level: name[1],
    name: name[0],
    status: state,
    startedAt,
    finishedAt: new Date(begin + 12_000).toISOString(),
    exitCode: state === 'passed' || state === 'not_applicable' ? 0 : 1,
    output,
    imageDigest: name[0] === 'sandbox' || name[0] === 'start' ? 'sha256:7f3a9c1e2b4d' : '',
  }
}

/** The stages of a pipeline that stops at [stoppedAt] (the index of the stage that did not pass) with [state]. */
function pipeline(
  begin: number,
  stoppedAt: number | null,
  state: Schemas['VerificationStageStatus'],
  output: string,
): Schemas['VerificationStage'][] {
  const stopped = stoppedAt ?? stageNames.length
  return stageNames.slice(0, Math.min(stopped + 1, stageNames.length)).map((name, index) => {
    const failed = stoppedAt !== null && index === stopped
    return stage(name, failed ? state : 'passed', begin + index * 15_000, failed ? output : '')
  })
}

const facts = [
  { name: 'tables', rule: 'exact', source: '42' },
  { name: 'rows:public.orders', rule: 'exact', source: '1204311' },
  { name: 'control:/srv/data', rule: 'exact', source: 'nonempty' },
  { name: 'database_size', rule: 'info', source: '943718400' },
] as const

type Fingerprint = Schemas['Verification']['fingerprint']

type FingerprintRow = Fingerprint[number]

/** What the copy holds of a field that differs. */
function differingCopy(name: string): string {
  return name.startsWith('rows') ? '1204310' : 'empty'
}

function fingerprintRow(fact: (typeof facts)[number], differs: boolean): FingerprintRow {
  const { name, rule, source } = fact
  if (rule === 'info') return { name, rule, source, copy: null, outcome: 'not_compared' }
  const differing = differs && name !== 'tables'
  return {
    name,
    rule,
    source,
    copy: differing ? differingCopy(name) : source,
    outcome: differing ? 'mismatched' : 'matched',
  }
}

/** The fingerprint of the fixtures' sources next to the copy's: one row of each outcome when [differs]. */
function fingerprint(differs: boolean): Fingerprint {
  return facts.map((fact) => fingerprintRow(fact, differs))
}

interface Spec {
  source: string
  status: Schemas['VerificationOutcome']
  level: number
  hoursAgo: number
  reason?: string
  restoreDurationMs?: number
  mismatches?: Schemas['FingerprintMismatch'][]
  stages?: Schemas['VerificationStage'][]
  unsuitable?: Schemas['VerifierMisfit'][]
  fingerprint?: Fingerprint
}

let counter = 0

// What a record says when the spec does not say it.
const BLANK = {
  reason: null,
  restoreDurationMs: null,
  mismatches: [],
  stages: [],
  unsuitable: null,
  fingerprint: [],
}

const stagesOf = (own: Partial<Spec>) => own.stages?.length ?? 0

function record(now: number, spec: Spec): Schemas['Verification'] {
  counter += 1
  const uuid = (base: number) =>
    `0192f7a0-0000-7000-8000-${String(base + counter).padStart(12, '0')}`
  const judged = spec.status !== 'no_verifier'
  const { hoursAgo, source, ...own } = spec
  return {
    ...BLANK,
    ...own,
    id: uuid(600),
    jobId: uuid(700),
    sourceId: source,
    snapshotId: ids.snapshot,
    resticSnapshotId: '4f3a9c1e7b2d5a60c8e1f2a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7',
    stepId: judged ? uuid(800) : null,
    verifierAgentId: judged ? ids.verifierAgent : null,
    keyHolders: 2,
    // The mocks' stages are 15 seconds each, one after another, as the pipeline above lays them.
    durationMs: stagesOf(own) === 0 ? null : stagesOf(own) * 15_000,
    recovered: false,
    verifiedAt: new Date(now - hoursAgo * HOUR).toISOString(),
    hiddenFacts: own.fingerprint === undefined ? 0 : 100,
  }
}

const mismatches: Schemas['FingerprintMismatch'][] = [
  {
    name: 'rows:public.orders',
    kind: 'rows',
    subject: 'public.orders',
    expected: '1204311',
    actual: '1204310',
  },
  {
    name: 'control:/srv/data',
    kind: 'control_path',
    subject: '/srv/data',
    expected: 'nonempty',
    actual: 'empty',
  },
]

const misfits: Schemas['VerifierMisfit'][] = [
  { agentId: ids.verifierAgent, hostname: 'verify6.example.com', reasons: ['offline', 'no_key'] },
]

/**
 * The history of the verification the fixtures hold (V6), newest first: a pass of level 5 and one of level 4, a
 * failure with mismatches, a check that could not run, an infrastructure failure and a job nobody could take. [sources]
 * are the live sources: the self-backup's are created at random and found by role.
 */
export function verificationRecords(
  now: number,
  sources: Schemas['Source'][],
): Schemas['Verification'][] {
  counter = 0
  const database = sources.find((s) => s.systemRole === 'self_database')
  const records: Spec[] = [
    {
      source: ids.etcSource,
      status: 'passed',
      level: 5,
      hoursAgo: 3,
      restoreDurationMs: 252_000,
      stages: pipeline(now - 3 * HOUR, null, 'passed', ''),
    },
    {
      source: ids.etcSource,
      status: 'passed',
      level: 4,
      hoursAgo: 27,
      stages: pipeline(now - 27 * HOUR, null, 'passed', ''),
    },
    {
      source: ids.etcSource,
      status: 'failed',
      level: 3,
      hoursAgo: 51,
      reason: 'the fingerprint of the copy differs from the source',
      mismatches,
      stages: pipeline(now - 51 * HOUR, 7, 'failed', 'fingerprint: 2 facts differ'),
      fingerprint: fingerprint(true),
    },
    {
      source: ids.etcSource,
      status: 'infrastructure_failure',
      level: 0,
      hoursAgo: 75,
      reason: 'three attempts without a verdict',
    },
    {
      source: ids.etcSource,
      status: 'no_verifier',
      level: 0,
      hoursAgo: 99,
      reason: 'no verifier fits the job',
      unsuitable: misfits,
    },
    {
      source: ids.homeSource,
      status: 'failed',
      level: 3,
      hoursAgo: 1,
      reason: 'the fingerprint of the copy differs from the source',
      mismatches,
      stages: pipeline(now - HOUR, 7, 'failed', 'fingerprint: 2 facts differ'),
      fingerprint: fingerprint(true),
    },
    {
      source: ids.homeSource,
      status: 'passed',
      level: 4,
      hoursAgo: 30,
      stages: pipeline(now - 30 * HOUR, null, 'passed', ''),
      fingerprint: fingerprint(false),
    },
    {
      source: ids.srvSource,
      status: 'cannot_verify',
      level: 1,
      hoursAgo: 2,
      reason: 'the restore stage could not run',
      stages: pipeline(
        now - 2 * HOUR,
        5,
        'cannot_run',
        'extension postgis is not in the sandbox image',
      ),
    },
    {
      source: ids.appSource,
      status: 'passed',
      level: 4,
      hoursAgo: 48,
      stages: pipeline(now - 48 * HOUR, null, 'passed', ''),
    },
  ]
  if (database !== undefined) {
    records.push({
      source: database.id,
      status: 'passed',
      level: 4,
      hoursAgo: 1,
      stages: pipeline(now - HOUR, null, 'passed', ''),
    })
  }
  return records
    .map((spec) => record(now, spec))
    .sort((a, b) => b.verifiedAt.localeCompare(a.verifiedAt))
}

/** The source as the fixtures leave it: its verification switch, and its metric from the newest records. */
export function withVerification(
  source: Schemas['Source'],
  records: Schemas['Verification'][],
): Schemas['Source'] {
  const own = records.filter((r) => r.sourceId === source.id)
  const lastPass = own.find((r) => r.status === 'passed')
  return {
    ...source,
    verificationEnabled: !verificationOff.has(source.id),
    verificationStatus: own[0]?.status ?? null,
    lastVerifiedAt: lastPass?.verifiedAt ?? null,
    lastVerifiedLevel: lastPass?.level ?? null,
  }
}
