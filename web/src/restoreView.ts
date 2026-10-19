// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

type Json = Record<string, unknown>

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
