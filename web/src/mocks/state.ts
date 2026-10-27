// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../api/schema'
import { freshTracker, type Tracker } from './lockout'
import { defaultSelfBackup } from './selfBackupSources'
import { verificationSchedule } from './verification'
import {
  agents,
  deletedSources,
  enrollmentTokens,
  runs,
  scheduleFires,
  schedules,
  snapshots,
  sources,
  MOCK_PASSWORD,
  stepLogs,
} from './fixtures'

type Schemas = components['schemas']

/** What the mock server remembers between requests; reset per test. */
export interface MockState {
  signedIn: boolean
  /** The administrator password; sign-in and the password change check it. */
  password: string
  /** Failed sign-ins (and wrong current passwords) and the lock they set (К4). */
  signIns: Tracker
  /** Wrong setup codes and the lock they set. */
  codes: Tracker
  onboarding: {
    /** The step ca is done. */
    caDone: boolean
    /** The step admin is done: there is an administrator. */
    adminDone: boolean
    /** The client entered the setup code (the sard_setup cookie, as the mocks see it). */
    setupSession: boolean
    /** The key copy is confirmed: the onboarding is complete (F4b). */
    keysDone: boolean
    /** The installation was upgraded with agents and sources: only new agents are held back (Р21). */
    upgraded: boolean
  }
  agents: Schemas['AgentDetails'][]
  sources: Schemas['Source'][]
  /** Deleted sources: gone from the list and the card, their runs and snapshots stay (soft delete, К13). */
  deletedSources: Schemas['Source'][]
  runs: Schemas['Run'][]
  /** One per source at most (F3a); a deleted source's schedule stays but is never shown. */
  schedules: Schemas['Schedule'][]
  /** The journal of each source's schedule, newest recorded first. */
  scheduleFires: Record<string, Schemas['ScheduleFire'][]>
  snapshots: Schemas['Snapshot'][]
  stepLogs: Record<string, Schemas['LogLine'][]>
  tokens: Schemas['EnrollmentToken'][]
  /** The self-backup's binding (F6); its agent and repository are those of the system sources. */
  selfBackup: { boundAt: string; location: string | null; keysConfirmedAt: string | null } | null
  /** The repository the neighbour will report: the one the last command was built for (Рк18). */
  pendingRepository: { name: string; backend: string; local: boolean; askedAt: number } | null
  /** How "back up now" ends, per run: when, and how (Рк18). */
  runOutcomes: Record<
    string,
    { at: number; status: 'succeeded' | 'failed'; message: string | null }
  >
  /** The first run of the database fails once with a message of the step (VITE_MOCK_SELF_BACKUP_FAIL=1). */
  failFirstBackup: boolean
  /** The open verification job of each source (V4): «verify now» makes one, a second is 409. */
  verificationJobs: Record<string, Schemas['VerificationJob']>
  /** Recorded verifications, newest first. */
  verifications: Schemas['Verification'][]
  verificationSchedule: Schemas['VerificationSchedule']
  /** The installation's default step timeouts (J1b): 6 h and 12 h until set. */
  runDefaults: Schemas['RunDefaults']
}

export interface ResetOptions {
  signedIn: boolean
  /** A clean installation: the first-start wizard, then the steps of the self-backup. */
  freshInstall?: boolean
  /** An installation upgraded with agents and sources: the onboarding is not complete, only tokens are refused. */
  upgraded?: boolean
  failFirstBackup?: boolean
}

/** The built-in agent of a clean or upgraded installation has initialised nothing yet (F4b, Рк18). */
function neighbourWithoutRepositories(list: Schemas['AgentDetails'][]): Schemas['AgentDetails'][] {
  return list.map((agent) => (agent.builtin ? { ...agent, repositories: [] } : agent))
}

function boundSelfBackup(complete: boolean) {
  return defaultSelfBackup(complete ? agents.find((agent) => agent.builtin)?.id : undefined)
}

function fresh(options: ResetOptions): MockState {
  const freshInstall = options.freshInstall ?? false
  const upgraded = options.upgraded ?? false
  const complete = !freshInstall && !upgraded
  // A complete installation has its self-backup bound to "self", backed up and confirmed (Рк18).
  const bound = boundSelfBackup(complete)
  return structuredClone({
    signedIn: options.signedIn,
    password: MOCK_PASSWORD,
    signIns: freshTracker(),
    codes: freshTracker(),
    onboarding: {
      caDone: !freshInstall,
      adminDone: !freshInstall,
      setupSession: false,
      keysDone: complete,
      upgraded,
    },
    agents: complete ? agents : neighbourWithoutRepositories(agents),
    sources: [...bound.sources, ...sources],
    deletedSources,
    // Newest first: the runs of the binding are older than every fixture run.
    runs: [...runs, ...bound.runs],
    schedules: [...schedules, ...bound.schedules],
    scheduleFires,
    snapshots,
    stepLogs,
    tokens: enrollmentTokens,
    selfBackup: bound.binding,
    pendingRepository: null,
    runOutcomes: {},
    failFirstBackup: options.failFirstBackup ?? false,
    verificationJobs: {},
    verifications: [],
    verificationSchedule,
    runDefaults: { backupTimeoutMinutes: 360, restoreTimeoutMinutes: 720 },
  })
}

export let state: MockState = fresh({ signedIn: false })

/**
 * Back to the fixtures. Both the dev worker and Vitest start signed out (К4); by default
 * the administrator exists, [freshInstall] starts a clean installation (setup code, no admin).
 */
export function resetMockState(options: ResetOptions): void {
  state = fresh(options)
}
