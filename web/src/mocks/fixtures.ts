// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { Status } from '../api/client'
import type { components } from '../api/schema'

type Schemas = components['schemas']

export const status: Status = {
  version: '0.0.0-mock',
  lastVerifiedRestoreAt: '2026-09-01T03:04:00Z',
}

// Sample data of the stage 1 API: agents online, offline, revoked, duplicated and
// never registered; runs in every state a step can end in; enrollment tokens in all
// four states. Every state of the W2 pages can be seen in them (docs/qa/console-pages.md).

/** The administrator password the mock accepts. */
export const MOCK_PASSWORD = 'admin'
export const TENANT_ID = '0192f7a0-0000-7000-8000-00000000000a'

export const ids = {
  dbAgent: '0192f7a0-0000-7000-8000-000000000101',
  webAgent: '0192f7a0-0000-7000-8000-000000000102',
  revokedAgent: '0192f7a0-0000-7000-8000-000000000103',
  duplicateAgent: '0192f7a0-0000-7000-8000-000000000104',
  bareAgent: '0192f7a0-0000-7000-8000-000000000105',
  selfAgent: '0192f7a0-0000-7000-8000-000000000106',
  etcSource: '0192f7a0-0000-7000-8000-000000000201',
  homeSource: '0192f7a0-0000-7000-8000-000000000202',
  srvSource: '0192f7a0-0000-7000-8000-000000000203',
  oldSource: '0192f7a0-0000-7000-8000-000000000204',
  succeededRun: '0192f7a0-0000-7000-8000-000000000301',
  failedRun: '0192f7a0-0000-7000-8000-000000000302',
  runningRun: '0192f7a0-0000-7000-8000-000000000303',
  queuedRun: '0192f7a0-0000-7000-8000-000000000304',
  partialRun: '0192f7a0-0000-7000-8000-000000000305',
  lostRun: '0192f7a0-0000-7000-8000-000000000306',
  rejectedRun: '0192f7a0-0000-7000-8000-000000000307',
  timedOutRun: '0192f7a0-0000-7000-8000-000000000308',
  deletedSourceRun: '0192f7a0-0000-7000-8000-000000000309',
  succeededStep: '0192f7a0-0000-7000-8000-000000000311',
  failedStep: '0192f7a0-0000-7000-8000-000000000312',
  runningStep: '0192f7a0-0000-7000-8000-000000000313',
  queuedStep: '0192f7a0-0000-7000-8000-000000000314',
  partialStep: '0192f7a0-0000-7000-8000-000000000315',
  lostStep: '0192f7a0-0000-7000-8000-000000000316',
  rejectedStep: '0192f7a0-0000-7000-8000-000000000317',
  timedOutStep: '0192f7a0-0000-7000-8000-000000000318',
  deletedSourceStep: '0192f7a0-0000-7000-8000-000000000319',
  snapshot: '0192f7a0-0000-7000-8000-000000000401',
  partialSnapshot: '0192f7a0-0000-7000-8000-000000000402',
  activeToken: '0192f7a0-0000-7000-8000-000000000501',
  usedToken: '0192f7a0-0000-7000-8000-000000000502',
  expiredToken: '0192f7a0-0000-7000-8000-000000000503',
  revokedToken: '0192f7a0-0000-7000-8000-000000000504',
}

// The files plugin's config schema exactly as the agent announces it
// (agent/plugins/files/schema.json); handlers.test.ts compares the two.
const filesSchema = {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  title: 'Files',
  type: 'object',
  properties: {
    paths: {
      type: 'array',
      title: 'Paths',
      description:
        'Absolute paths of the directories and files on the host to back up. At least one, at most 64, without repeats; a path may not lie inside another one of the list. The agent user must be able to read them.',
      'x-sard-i18n': {
        ru: {
          title: 'Пути',
          description:
            'Абсолютные пути каталогов и файлов на хосте, которые нужно сохранить. Не меньше одного и не больше 64, без повторов; один путь списка не может лежать внутри другого. Пользователь агента должен иметь право их читать.',
        },
      },
      items: { type: 'string', minLength: 1, maxLength: 4096, pattern: '^/[^\\x00]*$' },
      minItems: 1,
      maxItems: 64,
      uniqueItems: true,
      examples: [['/etc', '/var/www']],
    },
    exclude: {
      type: 'array',
      title: 'Exclude patterns',
      description:
        'restic patterns of files and directories to leave out of the snapshot, for example *.log. The syntax is described at https://restic.readthedocs.io/en/stable/040_backup.html#excluding-files',
      'x-sard-i18n': {
        ru: {
          title: 'Исключения',
          description:
            'Шаблоны restic для файлов и каталогов, которые не попадут в снимок, например *.log. Синтаксис описан в https://restic.readthedocs.io/en/stable/040_backup.html#excluding-files',
        },
      },
      items: { type: 'string', minLength: 1, maxLength: 1024, pattern: '^[^\\x00]+$' },
      maxItems: 256,
      examples: [['*.log', '/var/www/cache']],
    },
    one_file_system: {
      type: 'boolean',
      title: 'Stay on one file system',
      description:
        'When on, restic does not descend into directories that belong to other file systems (mounts). Off by default: restic crosses file system boundaries.',
      'x-sard-i18n': {
        ru: {
          title: 'Не выходить за пределы файловой системы',
          description:
            'Если включено, restic не заходит в каталоги других файловых систем (точки монтирования). По умолчанию выключено: restic переходит на другие файловые системы.',
        },
      },
      default: false,
      examples: [true],
    },
  },
  required: ['paths'],
  additionalProperties: false,
}

// A plugin whose schema has a secret field, a number and a choice: the form of a source shows them.
const postgresSchema = {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  type: 'object',
  required: ['database', 'password'],
  properties: {
    database: { type: 'string', title: 'Database' },
    port: { type: 'integer', title: 'Port', default: 5432 },
    password: { type: 'string', format: 'sard-secret', title: 'Password secret' },
    format: { type: 'string', enum: ['custom', 'plain'], title: 'Dump format' },
  },
}

// A schema the form cannot lay out (a nested object): the source is edited as JSON.
const nestedSchema = {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  type: 'object',
  properties: { target: { type: 'object', properties: { host: { type: 'string' } } } },
}

const filesPlugin = {
  name: 'files',
  version: '0.1.0',
  actions: ['backup', 'restore'] as Schemas['StepAction'][],
  configSchema: filesSchema,
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
    duplicateSessionAt: null,
    outdated: false,
    builtin: false,
    protocolVersion: 1,
    plugins: [
      filesPlugin,
      { name: 'postgres', version: '0.1.0', actions: ['backup'], configSchema: postgresSchema },
      { name: 'custom', version: '0.1.0', actions: ['backup'], configSchema: nestedSchema },
      { name: 'hooks', version: '0.1.0', actions: ['run'], configSchema: filesSchema },
    ],
    repositories: [
      { name: 'local', backend: 'local', repositoryId: '5a1f0c3e9b', cryptoProvider: 'file' },
      { name: 'offsite', backend: 'sftp', repositoryId: null, cryptoProvider: 'file' },
    ],
    secretNames: ['pg-password', 'api-token'],
    scriptNames: ['flush-caches'],
  },
  {
    id: ids.webAgent,
    hostname: 'web2.example.com',
    status: 'offline',
    agentVersion: 'v1.3.2',
    os: 'linux',
    arch: 'arm64',
    registeredAt: '2026-09-21T12:00:00Z',
    lastSeenAt: '2026-09-26T18:30:00Z',
    revokedAt: null,
    duplicateSessionAt: null,
    outdated: true,
    builtin: false,
    protocolVersion: 1,
    plugins: [{ ...filesPlugin, actions: ['backup'] }],
    repositories: [
      { name: 'local', backend: 'local', repositoryId: '9c7d2b1a04', cryptoProvider: 'file' },
    ],
    secretNames: [],
    scriptNames: [],
  },
  {
    id: ids.revokedAgent,
    hostname: 'old3.example.com',
    status: 'offline',
    agentVersion: '0.1.0',
    os: 'linux',
    arch: 'amd64',
    registeredAt: '2026-09-10T09:00:00Z',
    lastSeenAt: '2026-09-15T10:00:00Z',
    revokedAt: '2026-09-16T08:00:00Z',
    duplicateSessionAt: null,
    outdated: false,
    builtin: false,
    protocolVersion: 1,
    plugins: [filesPlugin],
    repositories: [
      { name: 'local', backend: 'local', repositoryId: '7e5d4c3b2a', cryptoProvider: 'file' },
    ],
    secretNames: [],
    scriptNames: [],
  },
  {
    id: ids.duplicateAgent,
    hostname: 'clone4.example.com',
    status: 'offline',
    agentVersion: '0.1.0',
    os: 'linux',
    arch: 'amd64',
    registeredAt: '2026-09-22T09:00:00Z',
    lastSeenAt: '2026-09-27T08:00:00Z',
    revokedAt: null,
    duplicateSessionAt: '2026-09-27T08:05:00Z',
    outdated: false,
    builtin: false,
    protocolVersion: 1,
    plugins: [filesPlugin],
    repositories: [
      { name: 'local', backend: 'local', repositoryId: '5a1f0c3e9b', cryptoProvider: 'file' },
    ],
    secretNames: [],
    scriptNames: [],
  },
  {
    id: ids.bareAgent,
    hostname: 'new5.example.com',
    status: 'offline',
    agentVersion: null,
    os: null,
    arch: null,
    registeredAt: '2026-09-27T09:30:00Z',
    lastSeenAt: null,
    revokedAt: null,
    duplicateSessionAt: null,
    outdated: false,
    builtin: false,
    protocolVersion: null,
    plugins: [],
    repositories: [],
    secretNames: [],
    scriptNames: [],
  },
  {
    id: ids.selfAgent,
    hostname: 'sard-self',
    status: 'online',
    agentVersion: '0.1.0',
    os: 'linux',
    arch: 'amd64',
    registeredAt: '2026-09-27T09:40:00Z',
    lastSeenAt: '2026-09-27T09:59:55Z',
    revokedAt: null,
    duplicateSessionAt: null,
    outdated: false,
    builtin: true,
    protocolVersion: 1,
    plugins: [filesPlugin],
    // F6: an S3 repository for the self-backup, a local one (D10: needs a confirmation), one not initialised.
    repositories: [
      { name: 'offsite', backend: 's3', repositoryId: '3b8e1f6a2c', cryptoProvider: 'file' },
      { name: 'disk', backend: 'local', repositoryId: '6d2a9e4f1b', cryptoProvider: 'file' },
      { name: 'fresh', backend: 'sftp', repositoryId: null, cryptoProvider: 'file' },
    ],
    secretNames: ['sard-db'],
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
    systemRole: null,
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
    systemRole: null,
  },
  {
    id: ids.srvSource,
    name: 'web2 /srv',
    agentId: ids.webAgent,
    plugin: 'files',
    repositoryName: 'local',
    config: { paths: ['/srv'] },
    createdAt: '2026-09-27T09:50:00Z',
    updatedAt: '2026-09-27T09:50:00Z',
    systemRole: null,
  },
]

/** A source that was deleted: gone from the list and the card, its run stays. */
export const deletedSources: Schemas['Source'][] = [
  {
    id: ids.oldSource,
    name: 'db1 old',
    agentId: ids.dbAgent,
    plugin: 'files',
    repositoryName: 'local',
    config: { paths: ['/old'] },
    createdAt: '2026-09-18T10:00:00Z',
    updatedAt: '2026-09-18T10:00:00Z',
    systemRole: null,
  },
]

interface StepFacts {
  id: string
  status: Schemas['StepStatus']
  queuedAt: string
  startedAt: string | null
  finishedAt: string | null
  sourceId?: string
  agentId?: string
}

function backupStep(facts: StepFacts): Schemas['RunStep'] {
  return {
    id: facts.id,
    ordinal: 0,
    action: 'backup',
    status: facts.status,
    phase: 'accepted',
    agentId: facts.agentId ?? ids.dbAgent,
    sourceId: facts.sourceId ?? ids.etcSource,
    plugin: 'files',
    repositoryName: 'local',
    bytesProcessed: null,
    bytesTotal: null,
    filesProcessed: null,
    filesTotal: null,
    message: null,
    backup: null,
    queuedAt: facts.queuedAt,
    dispatchedAt: facts.startedAt === null ? null : facts.queuedAt,
    startedAt: facts.startedAt,
    finishedAt: facts.finishedAt,
  }
}

const NAMES: Record<string, string> = {
  [ids.etcSource]: 'db1 /etc',
  [ids.homeSource]: 'web2 /home',
  [ids.srvSource]: 'web2 /srv',
  [ids.oldSource]: 'db1 old',
}

/** A run of [sourceId] with one backup step; the run's times and message follow the step's. */
function runOf(
  id: string,
  step: Schemas['RunStep'],
  status: Schemas['RunStatus'],
  sourceDeleted = false,
): Schemas['Run'] {
  const sourceId = step.sourceId ?? ids.etcSource
  return {
    id,
    sourceId,
    sourceName: NAMES[sourceId],
    sourceDeleted,
    agentId: step.agentId,
    trigger: 'manual',
    status,
    message: step.message,
    queuedAt: step.queuedAt,
    startedAt: step.startedAt,
    finishedAt: step.finishedAt,
    steps: [step],
  }
}

const snapshotOutput = {
  snapshotId: 'a1b2c3',
  totalBytes: 31_457_280,
  addedBytes: 524_288,
  repositoryId: '5a1f0c3e9b',
}

// Newest first, as the list endpoint returns them.
export const runs: Schemas['Run'][] = [
  // Queued for an agent that is offline: it starts when the agent connects.
  runOf(
    ids.queuedRun,
    // Nothing is reported before the agent accepts the step.
    {
      ...backupStep({
        id: ids.queuedStep,
        status: 'queued',
        queuedAt: '2026-09-27T09:59:00Z',
        startedAt: null,
        finishedAt: null,
        sourceId: ids.srvSource,
        agentId: ids.webAgent,
      }),
      phase: null,
    },
    'queued',
  ),
  runOf(
    ids.runningRun,
    {
      ...backupStep({
        id: ids.runningStep,
        status: 'running',
        queuedAt: '2026-09-27T09:58:00Z',
        startedAt: '2026-09-27T09:58:02Z',
        finishedAt: null,
      }),
      phase: 'uploading',
      bytesProcessed: 734_003_200,
      bytesTotal: 2_147_483_648,
      filesProcessed: 1_200,
      filesTotal: 3_400,
    },
    'running',
  ),
  runOf(
    ids.failedRun,
    {
      ...backupStep({
        id: ids.failedStep,
        status: 'failed',
        queuedAt: '2026-09-26T21:00:00Z',
        startedAt: '2026-09-26T21:00:01Z',
        finishedAt: '2026-09-26T21:00:04Z',
      }),
      phase: 'dumping',
      message: 'open /etc/shadow: permission denied',
    },
    'failed',
  ),
  // Failed after it saved a snapshot: the snapshot is usable but incomplete.
  runOf(
    ids.partialRun,
    {
      ...backupStep({
        id: ids.partialStep,
        status: 'failed',
        queuedAt: '2026-09-26T15:00:00Z',
        startedAt: '2026-09-26T15:00:01Z',
        finishedAt: '2026-09-26T15:01:10Z',
      }),
      phase: 'uploading',
      bytesProcessed: 31_457_280,
      bytesTotal: 31_457_280,
      message: '2 files unreadable',
      backup: { ...snapshotOutput, partial: true },
    },
    'failed',
  ),
  // Lost while running, then the agent's late result arrived with a complete snapshot.
  runOf(
    ids.lostRun,
    {
      ...backupStep({
        id: ids.lostStep,
        status: 'lost',
        queuedAt: '2026-09-26T12:00:00Z',
        startedAt: '2026-09-26T12:00:01Z',
        finishedAt: '2026-09-26T12:05:00Z',
      }),
      phase: 'uploading',
      message: 'agent lost the step',
      backup: { ...snapshotOutput, snapshotId: 'd4e5f6', partial: false },
    },
    'failed',
  ),
  runOf(
    ids.rejectedRun,
    {
      ...backupStep({
        id: ids.rejectedStep,
        status: 'rejected',
        queuedAt: '2026-09-26T10:00:00Z',
        startedAt: null,
        finishedAt: '2026-09-26T10:00:02Z',
      }),
      phase: null,
      message: 'unknown plugin files',
    },
    'failed',
  ),
  runOf(
    ids.timedOutRun,
    {
      ...backupStep({
        id: ids.timedOutStep,
        status: 'timed_out',
        queuedAt: '2026-09-26T08:00:00Z',
        startedAt: '2026-09-26T08:00:01Z',
        finishedAt: '2026-09-26T09:00:01Z',
      }),
      phase: 'uploading',
      message: 'deadline exceeded',
    },
    'failed',
  ),
  runOf(
    ids.succeededRun,
    {
      ...backupStep({
        id: ids.succeededStep,
        status: 'succeeded',
        queuedAt: '2026-09-25T21:00:00Z',
        startedAt: '2026-09-25T21:00:01Z',
        finishedAt: '2026-09-25T21:02:31Z',
      }),
      phase: 'uploading',
      bytesProcessed: 52_428_800,
      bytesTotal: 52_428_800,
      backup: {
        snapshotId: '4f1c2a9e',
        totalBytes: 52_428_800,
        addedBytes: 1_048_576,
        repositoryId: '5a1f0c3e9b',
        partial: false,
      },
    },
    'succeeded',
  ),
  // The run of a source that was deleted afterwards.
  runOf(
    ids.deletedSourceRun,
    {
      ...backupStep({
        id: ids.deletedSourceStep,
        status: 'succeeded',
        queuedAt: '2026-09-20T10:00:00Z',
        startedAt: '2026-09-20T10:00:01Z',
        finishedAt: '2026-09-20T10:01:00Z',
        sourceId: ids.oldSource,
      }),
      phase: 'uploading',
      backup: { ...snapshotOutput, snapshotId: '0a0b0c', partial: false },
    },
    'succeeded',
    true,
  ),
]

export const snapshots: Schemas['Snapshot'][] = [
  {
    id: ids.partialSnapshot,
    snapshotId: 'a1b2c3',
    sourceId: ids.etcSource,
    runId: ids.partialRun,
    stepId: ids.partialStep,
    agentId: ids.dbAgent,
    repositoryName: 'local',
    repositoryId: '5a1f0c3e9b',
    totalBytes: 31_457_280,
    addedBytes: 524_288,
    createdAt: '2026-09-26T15:01:09Z',
    forgottenAt: null,
    partial: true,
  },
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
    partial: false,
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
  [ids.partialStep]: [
    { seq: 1, time: '2026-09-26T15:00:01Z', level: 'info', text: 'scanning /etc' },
    {
      seq: 2,
      time: '2026-09-26T15:00:30Z',
      level: 'info',
      text: 'connecting with password [REDACTED]',
    },
    {
      seq: 3,
      time: '2026-09-26T15:01:09Z',
      level: 'warn',
      text: 'snapshot a1b2c3 saved, 2 files unreadable',
    },
  ],
  // The server cut this log at its size limit: the last line is its own, without a time.
  [ids.timedOutStep]: [
    { seq: 1, time: '2026-09-26T08:00:01Z', level: 'info', text: 'scanning /var' },
    {
      seq: 2,
      time: null,
      level: 'warn',
      text: 'log truncated at 16777216 bytes; later lines of this step are dropped',
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
    label: null,
  },
  {
    id: ids.revokedToken,
    status: 'revoked',
    createdAt: '2026-09-24T09:00:00Z',
    expiresAt: '2026-09-25T09:00:00Z',
    usedAt: null,
    revokedAt: '2026-09-24T09:30:00Z',
    agentId: null,
    label: null,
  },
  {
    id: ids.expiredToken,
    status: 'expired',
    createdAt: '2026-09-21T09:00:00Z',
    expiresAt: '2026-09-22T09:00:00Z',
    usedAt: null,
    revokedAt: null,
    agentId: null,
    label: null,
  },
  {
    id: ids.usedToken,
    status: 'used',
    createdAt: '2026-09-20T08:00:00Z',
    expiresAt: '2026-09-21T08:00:00Z',
    usedAt: '2026-09-20T09:00:00Z',
    revokedAt: null,
    agentId: ids.dbAgent,
    label: null,
  },
]

/** The /etc source backs up nightly at 21:00 in Berlin; its journal shows each kind of fire (F3a). */
export const schedules: Schemas['Schedule'][] = [
  {
    id: '0192f7a0-0000-7000-8000-000000000601',
    sourceId: ids.etcSource,
    cron: '0 21 * * *',
    timezone: 'Europe/Berlin',
    enabled: true,
    nextRunAt: '2026-09-27T19:00:00Z',
    catchUpAt: null,
    lastFiredAt: '2026-09-26T19:00:00Z',
    skippedInRow: 0,
    createdAt: '2026-09-22T10:05:00Z',
    updatedAt: '2026-09-22T10:05:00Z',
  },
]

function fire(
  id: string,
  outcome: Schemas['ScheduleFireOutcome'],
  scheduledFor: string,
  fields: Partial<Schemas['ScheduleFire']> = {},
): Schemas['ScheduleFire'] {
  return {
    id,
    kind: 'schedule',
    scheduledFor,
    outcome,
    runId: null,
    reason: null,
    missedCount: null,
    missedUntil: null,
    skippedInRow: 0,
    alert: false,
    recordedAt: scheduledFor,
    ...fields,
  }
}

/** Newest recorded first, as the server lists them. */
export const scheduleFires: Record<string, Schemas['ScheduleFire'][]> = {
  [ids.etcSource]: [
    fire('0192f7a0-0000-7000-8000-000000000611', 'run_created', '2026-09-26T19:00:00Z', {
      runId: ids.failedRun,
    }),
    fire('0192f7a0-0000-7000-8000-000000000612', 'run_created', '2026-09-25T19:00:00Z', {
      kind: 'catch_up',
      runId: ids.succeededRun,
    }),
    fire('0192f7a0-0000-7000-8000-000000000613', 'skipped_downtime', '2026-09-23T19:00:00Z', {
      missedCount: 2,
      missedUntil: '2026-09-24T19:00:00Z',
      recordedAt: '2026-09-25T18:59:50Z',
    }),
  ],
}
