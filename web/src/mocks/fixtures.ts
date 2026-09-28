// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { Status } from '../api/client'
import type { components } from '../api/schema'

type Schemas = components['schemas']

export const status: Status = {
  version: '0.0.0-mock',
  lastVerifiedRestoreAt: '2026-09-01T03:04:00Z',
}

// Sample data of the stage 1 API: an online and an offline agent, runs that
// succeeded, failed and are running, enrollment tokens in all four states.

/** The administrator password the mock accepts. */
export const MOCK_PASSWORD = 'admin'
export const TENANT_ID = '0192f7a0-0000-7000-8000-00000000000a'

export const ids = {
  dbAgent: '0192f7a0-0000-7000-8000-000000000101',
  webAgent: '0192f7a0-0000-7000-8000-000000000102',
  etcSource: '0192f7a0-0000-7000-8000-000000000201',
  homeSource: '0192f7a0-0000-7000-8000-000000000202',
  succeededRun: '0192f7a0-0000-7000-8000-000000000301',
  failedRun: '0192f7a0-0000-7000-8000-000000000302',
  runningRun: '0192f7a0-0000-7000-8000-000000000303',
  succeededStep: '0192f7a0-0000-7000-8000-000000000311',
  failedStep: '0192f7a0-0000-7000-8000-000000000312',
  runningStep: '0192f7a0-0000-7000-8000-000000000313',
  snapshot: '0192f7a0-0000-7000-8000-000000000401',
  activeToken: '0192f7a0-0000-7000-8000-000000000501',
  usedToken: '0192f7a0-0000-7000-8000-000000000502',
  expiredToken: '0192f7a0-0000-7000-8000-000000000503',
  revokedToken: '0192f7a0-0000-7000-8000-000000000504',
}

const filesSchema = {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  type: 'object',
  required: ['paths'],
  properties: {
    paths: { type: 'array', items: { type: 'string' }, minItems: 1 },
    exclude: { type: 'array', items: { type: 'string' } },
  },
}

export const agents: Schemas['AgentDetails'][] = [
  {
    id: ids.dbAgent,
    hostname: 'db1.example.com',
    status: 'online',
    agentVersion: '0.1.0',
    os: 'linux',
    arch: 'amd64',
    registeredAt: '2026-09-20T09:00:00Z',
    lastSeenAt: '2026-09-27T09:59:50Z',
    revokedAt: null,
    protocolVersion: 1,
    plugins: [
      {
        name: 'files',
        version: '0.1.0',
        actions: ['backup', 'restore'],
        configSchema: filesSchema,
      },
    ],
    repositories: [
      { name: 'local', backend: 'local', repositoryId: '5a1f0c3e9b', cryptoProvider: 'file' },
      { name: 'offsite', backend: 'sftp', repositoryId: null, cryptoProvider: 'file' },
    ],
    secretNames: ['pg-password'],
    scriptNames: ['flush-caches'],
  },
  {
    id: ids.webAgent,
    hostname: 'web2.example.com',
    status: 'offline',
    agentVersion: '0.1.0',
    os: 'linux',
    arch: 'arm64',
    registeredAt: '2026-09-21T12:00:00Z',
    lastSeenAt: '2026-09-26T18:30:00Z',
    revokedAt: null,
    protocolVersion: 1,
    plugins: [{ name: 'files', version: '0.1.0', actions: ['backup'], configSchema: filesSchema }],
    repositories: [
      { name: 'local', backend: 'local', repositoryId: '9c7d2b1a04', cryptoProvider: 'file' },
    ],
    secretNames: [],
    scriptNames: [],
  },
]

export const sources: Schemas['Source'][] = [
  {
    id: ids.etcSource,
    name: 'db1 /etc',
    agentId: ids.dbAgent,
    plugin: 'files',
    repositoryName: 'local',
    config: { paths: ['/etc'] },
    createdAt: '2026-09-22T10:00:00Z',
    updatedAt: '2026-09-22T10:00:00Z',
  },
  {
    id: ids.homeSource,
    name: 'web2 /home',
    agentId: ids.webAgent,
    plugin: 'files',
    repositoryName: 'local',
    config: { paths: ['/home'], exclude: ['/home/*/.cache'] },
    createdAt: '2026-09-23T11:00:00Z',
    updatedAt: '2026-09-24T08:15:00Z',
  },
]

function backupStep(
  id: string,
  status: Schemas['StepStatus'],
  times: { queuedAt: string; startedAt: string; finishedAt: string | null },
): Schemas['RunStep'] {
  return {
    id,
    ordinal: 1,
    action: 'backup',
    status,
    phase: 'accepted',
    agentId: ids.dbAgent,
    sourceId: ids.etcSource,
    plugin: 'files',
    repositoryName: 'local',
    bytesProcessed: null,
    bytesTotal: null,
    message: null,
    backup: null,
    queuedAt: times.queuedAt,
    dispatchedAt: times.queuedAt,
    startedAt: times.startedAt,
    finishedAt: times.finishedAt,
  }
}

// Newest first, as the list endpoint returns them.
export const runs: Schemas['Run'][] = [
  {
    id: ids.runningRun,
    sourceId: ids.etcSource,
    agentId: ids.dbAgent,
    trigger: 'manual',
    status: 'running',
    message: null,
    queuedAt: '2026-09-27T09:58:00Z',
    startedAt: '2026-09-27T09:58:02Z',
    finishedAt: null,
    steps: [
      {
        ...backupStep(ids.runningStep, 'running', {
          queuedAt: '2026-09-27T09:58:00Z',
          startedAt: '2026-09-27T09:58:02Z',
          finishedAt: null,
        }),
        phase: 'uploading',
        bytesProcessed: 734_003_200,
        bytesTotal: 2_147_483_648,
      },
    ],
  },
  {
    id: ids.failedRun,
    sourceId: ids.etcSource,
    agentId: ids.dbAgent,
    trigger: 'manual',
    status: 'failed',
    message: 'open /etc/shadow: permission denied',
    queuedAt: '2026-09-26T21:00:00Z',
    startedAt: '2026-09-26T21:00:01Z',
    finishedAt: '2026-09-26T21:00:04Z',
    steps: [
      {
        ...backupStep(ids.failedStep, 'failed', {
          queuedAt: '2026-09-26T21:00:00Z',
          startedAt: '2026-09-26T21:00:01Z',
          finishedAt: '2026-09-26T21:00:04Z',
        }),
        phase: 'dumping',
        message: 'open /etc/shadow: permission denied',
      },
    ],
  },
  {
    id: ids.succeededRun,
    sourceId: ids.etcSource,
    agentId: ids.dbAgent,
    trigger: 'manual',
    status: 'succeeded',
    message: null,
    queuedAt: '2026-09-25T21:00:00Z',
    startedAt: '2026-09-25T21:00:01Z',
    finishedAt: '2026-09-25T21:02:31Z',
    steps: [
      {
        ...backupStep(ids.succeededStep, 'succeeded', {
          queuedAt: '2026-09-25T21:00:00Z',
          startedAt: '2026-09-25T21:00:01Z',
          finishedAt: '2026-09-25T21:02:31Z',
        }),
        phase: 'uploading',
        bytesProcessed: 52_428_800,
        bytesTotal: 52_428_800,
        backup: { snapshotId: '4f1c2a9e', totalBytes: 52_428_800, addedBytes: 1_048_576 },
      },
    ],
  },
]

export const snapshots: Schemas['Snapshot'][] = [
  {
    id: ids.snapshot,
    snapshotId: '4f1c2a9e',
    sourceId: ids.etcSource,
    runId: ids.succeededRun,
    stepId: ids.succeededStep,
    agentId: ids.dbAgent,
    repositoryName: 'local',
    repositoryId: '5a1f0c3e9b',
    totalBytes: 52_428_800,
    addedBytes: 1_048_576,
    createdAt: '2026-09-25T21:02:30Z',
    forgottenAt: null,
  },
]

const levels: Schemas['LogLevel'][] = ['debug', 'info', 'info', 'info', 'warn']

/** count lines of a step's log, seq 1..count, one second apart. */
export function logLines(
  count: number,
  from = Date.parse('2026-09-27T09:58:02Z'),
): Schemas['LogLine'][] {
  return Array.from({ length: count }, (_, i) => ({
    seq: i + 1,
    time: new Date(from + i * 1000).toISOString(),
    level: levels[i % levels.length],
    text: `backed up file ${i + 1} of the running step`,
  }))
}

/** Log lines by step id; the running step has enough to page through. */
export const stepLogs: Record<string, Schemas['LogLine'][]> = {
  [ids.runningStep]: logLines(350),
  [ids.failedStep]: [
    { seq: 1, time: '2026-09-26T21:00:01Z', level: 'info', text: 'scanning /etc' },
    {
      seq: 2,
      time: '2026-09-26T21:00:04Z',
      level: 'error',
      text: 'open /etc/shadow: permission denied',
    },
  ],
  [ids.succeededStep]: [
    { seq: 1, time: '2026-09-25T21:00:01Z', level: 'info', text: 'scanning /etc' },
    { seq: 2, time: '2026-09-25T21:02:30Z', level: 'info', text: 'snapshot 4f1c2a9e saved' },
  ],
}

// Newest first.
export const enrollmentTokens: Schemas['EnrollmentToken'][] = [
  {
    id: ids.activeToken,
    status: 'active',
    createdAt: '2026-09-27T09:00:00Z',
    expiresAt: '2026-09-28T09:00:00Z',
    usedAt: null,
    revokedAt: null,
    agentId: null,
  },
  {
    id: ids.revokedToken,
    status: 'revoked',
    createdAt: '2026-09-24T09:00:00Z',
    expiresAt: '2026-09-25T09:00:00Z',
    usedAt: null,
    revokedAt: '2026-09-24T09:30:00Z',
    agentId: null,
  },
  {
    id: ids.expiredToken,
    status: 'expired',
    createdAt: '2026-09-21T09:00:00Z',
    expiresAt: '2026-09-22T09:00:00Z',
    usedAt: null,
    revokedAt: null,
    agentId: null,
  },
  {
    id: ids.usedToken,
    status: 'used',
    createdAt: '2026-09-20T08:00:00Z',
    expiresAt: '2026-09-21T08:00:00Z',
    usedAt: '2026-09-20T09:00:00Z',
    revokedAt: null,
    agentId: ids.dbAgent,
  },
]
