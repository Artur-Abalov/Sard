// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'

type Schemas = components['schemas']

const CANCELLABLE: Schemas['RunOutcome'][] = [
  'queued',
  'waiting_for_agent',
  'dispatched',
  'running',
]

// The cancel button is for an active run: the server's outcome says so (КВJ2). A restore verification is
// cancelled as a job, from the tab «Verifications» of its source (J1f, ВJf1), not as a run.
export function cancellable(run: {
  outcome: Schemas['RunOutcome']
  trigger: Schemas['RunTrigger']
}): boolean {
  return CANCELLABLE.includes(run.outcome) && run.trigger !== 'verification'
}

// The timeout the step that timed out was created with; null when it is not known.
export function timeoutOfRun(
  steps: readonly Pick<Schemas['RunStep'], 'status' | 'timeoutSeconds'>[],
): number | null {
  return steps.find((step) => step.status === 'timed_out')?.timeoutSeconds ?? null
}
