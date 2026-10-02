// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../api/schema'

type Schemas = components['schemas']

export interface Page<T> {
  items: T[]
  nextCursor: string | null
}

/** A cursor page of items. The mock's cursor is the offset of the next item; clients treat it as opaque. */
export function pageOf<T>(items: T[], cursor: string | null, limit: number): Page<T> {
  const start = cursor === null ? 0 : Number(cursor)
  const end = start + limit
  return { items: items.slice(start, end), nextCursor: end < items.length ? String(end) : null }
}

/** Lines with seq > afterSeq, at most limit; lines are in seq order. */
export function logPage(
  lines: Schemas['LogLine'][],
  afterSeq: number,
  limit: number,
): Schemas['LogPage'] {
  const after = lines.filter((line) => line.seq > afterSeq)
  const items = after.slice(0, limit)
  return {
    items,
    nextAfterSeq: items.length > 0 ? items[items.length - 1].seq : afterSeq,
    hasMore: after.length > limit,
    // The server cuts a log at its size limit and ends it with a line of its own, which has no time.
    truncated: lines.at(-1)?.time === null,
  }
}
