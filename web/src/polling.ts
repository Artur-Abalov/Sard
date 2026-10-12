// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'

type Schemas = components['schemas']

// How often an active run, step or token is asked about again (OB1).
export const POLL_INTERVAL_MS = 3000

type Interval = typeof POLL_INTERVAL_MS | false

const ACTIVE_RUN: Schemas['RunStatus'][] = ['queued', 'dispatched', 'running']
const ACTIVE_STEP: Schemas['StepStatus'][] = ['queued', 'dispatched', 'running']

// TanStack Query's refetchInterval: the interval while there is activity, false otherwise.
export function runPollInterval(status: Schemas['RunStatus'] | undefined): Interval {
  return status !== undefined && ACTIVE_RUN.includes(status) ? POLL_INTERVAL_MS : false
}

export function stepPollInterval(status: Schemas['StepStatus'] | undefined): Interval {
  return status !== undefined && ACTIVE_STEP.includes(status) ? POLL_INTERVAL_MS : false
}

export function runListPollInterval(
  runs: { status: Schemas['RunStatus'] }[] | undefined,
): Interval {
  return runs?.some((run) => runPollInterval(run.status) !== false) === true
    ? POLL_INTERVAL_MS
    : false
}

export function tokenPollInterval(status: Schemas['EnrollmentTokenStatus'] | undefined): Interval {
  return status === 'active' ? POLL_INTERVAL_MS : false
}

// The card of a source asks again while the run its schedule created last is active (F3b).
export function schedulePollInterval(
  schedule: { lastRun: { status: Schemas['RunStatus'] } | null } | undefined,
): Interval {
  return runPollInterval(schedule?.lastRun?.status)
}
