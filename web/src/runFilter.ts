// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'

type RunStatus = components['schemas']['RunStatus']

export interface RunFilter {
  statuses: RunStatus[]
  sourceId: string | null
}

export const RUN_STATUSES: RunStatus[] = [
  'queued',
  'dispatched',
  'running',
  'succeeded',
  'failed',
  'cancelled',
]

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

function isStatus(value: unknown): value is RunStatus {
  return RUN_STATUSES.includes(value as RunStatus)
}

// The filter of the runs page from the parameters of its address. Whatever is not a
// run status or a UUID is dropped: an address someone edited never breaks the page.
export function parseRunFilter(search: Record<string, unknown>): RunFilter {
  const raw: unknown[] = Array.isArray(search.status) ? search.status : [search.status]
  const { sourceId } = search
  return {
    statuses: [...new Set(raw.filter(isStatus))],
    sourceId: typeof sourceId === 'string' && UUID.test(sourceId) ? sourceId : null,
  }
}
