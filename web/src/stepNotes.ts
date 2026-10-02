// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'

type RunStep = components['schemas']['RunStep']

// The server says whether a snapshot is incomplete (BackupOutput.partial); the
// console never infers it from the status of the step (G3).
export function partialNoteShown(step: Pick<RunStep, 'status' | 'backup'>): boolean {
  return step.backup?.partial === true
}

// A lost or rejected step has a sentence of its own before the server's reason (the reason alone
// does not say what kind of end it was).
export function stepNoteShown(status: components['schemas']['StepStatus']): boolean {
  return status === 'lost' || status === 'rejected'
}

// A queued run starts when its agent connects: the note shows two facts of the server, the run's
// status and the agent's (G7); the console decides nothing else.
export function waitsForAgent(
  run: components['schemas']['RunStatus'],
  agent: components['schemas']['AgentStatus'] | undefined,
): boolean {
  return run === 'queued' && agent === 'offline'
}
