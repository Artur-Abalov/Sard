// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'

type Schemas = components['schemas']
type Json = Record<string, unknown>

export type RestoreOutcome =
  | { kind: 'restored'; restore: Schemas['RestoreOutput'] }
  | { kind: 'nothing' }
  | { kind: 'rollbackFailed'; leftover: string }
  | { kind: 'late'; target: string }

const NOT_RESTORED: Schemas['StepStatus'][] = ['failed', 'rejected', 'timed_out', 'cancelled']

type Restore = Schemas['RestoreOutput'] | null

function rollbackFailed(restore: Restore): RestoreOutcome | null {
  const leftover = restore?.leftover ?? ''
  return leftover === '' ? null : { kind: 'rollbackFailed', leftover }
}

function finished(status: Schemas['StepStatus'], restore: Restore): RestoreOutcome | null {
  if (status === 'succeeded') return restore && { kind: 'restored', restore }
  if (status === 'lost') return restore?.target ? { kind: 'late', target: restore.target } : null
  return NOT_RESTORED.includes(status) ? { kind: 'nothing' } : null
}

// What the page of a restore step says about its end, from the server's facts only: a restore is
// all or nothing, so a failed step restored nothing unless the server names a leftover of its rollback.
export function restoreOutcome(step: {
  status: Schemas['StepStatus']
  restore: Restore
}): RestoreOutcome | null {
  return rollbackFailed(step.restore) ?? finished(step.status, step.restore)
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

function configOf(text: unknown): Json | null {
  if (typeof text !== 'string') return null
  try {
    const value: unknown = JSON.parse(text)
    return typeof value === 'object' && value !== null && !Array.isArray(value)
      ? (value as Json)
      : null
  } catch {
    return null
  }
}

// The agent and config the address of the restore page carries (the retry of a run); what is not
// valid is dropped, so an address someone edited never breaks the page.
export function parseRestoreSearch(search: Record<string, unknown>): {
  agentId: string | null
  config: Json | null
} {
  const { agentId } = search
  return {
    agentId: typeof agentId === 'string' && UUID.test(agentId) ? agentId : null,
    config: configOf(search.config),
  }
}

// The address of the restore page for the retry of a restore run: the same agent and config.
export function restoreSearchOf(run: { agentId: string; restore: { config: Json } }): {
  agentId: string
  config: string
} {
  return { agentId: run.agentId, config: JSON.stringify(run.restore.config) }
}

interface Request {
  agentId: string
  config: Json
}

// A preview answers the request it was made for: any other agent or config needs a new one.
export function restorePreviewStale(previewed: Request | null, current: Request): boolean {
  return (
    previewed === null ||
    previewed.agentId !== current.agentId ||
    JSON.stringify(previewed.config) !== JSON.stringify(current.config)
  )
}
