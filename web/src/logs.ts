// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'

type LogLine = components['schemas']['LogLine']

// The shown lines plus a page of the log, each seq once, in seq order.
export function mergeLogLines(shown: LogLine[], page: LogLine[]): LogLine[] {
  const bySeq = new Map(shown.map((line) => [line.seq, line]))
  for (const line of page) {
    bySeq.set(line.seq, line)
  }
  return [...bySeq.values()].sort((a, b) => a.seq - b.seq)
}
