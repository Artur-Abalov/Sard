// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'

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

type IncompleteReason = components['schemas']['IncompleteReason'] | null

export type BackupExplanation = {
  noteKey: string | null
  permissionHint: boolean
  messageShown: boolean
}

// The mark beside a run's status follows the server's flag alone (G10); never the status.
export function incompleteMarkShown(run: { partial: boolean }): boolean {
  return run.partial
}

// The key of the sentence about an incomplete snapshot, chosen by the server's reason.
export function snapshotNoteKey(reason: IncompleteReason): string | null {
  return reason === null ? null : `incomplete.${reason}`
}

// What a backup card says by the reason: the sentence about the snapshot, whether to point at the
// permissions of sard-agent, and whether the agent's own message is shown (not for unreadable
// files: the card names them itself).
export function backupExplanation(
  backup: { incompleteReason: IncompleteReason } | null,
): BackupExplanation {
  const reason = backup?.incompleteReason ?? null
  return {
    noteKey: snapshotNoteKey(reason),
    permissionHint: reason === 'unreadable_files',
    messageShown: reason !== 'unreadable_files',
  }
}

// How many unreadable files are not among the listed paths; null when nothing is left to say.
export function unreadableRemainder(count: number | null, listed: number): number | null {
  if (count === null || listed === 0) return null
  const rest = count - listed
  return rest > 0 ? rest : null
}
