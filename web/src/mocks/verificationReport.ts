// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../api/schema'

type Schemas = components['schemas']

/**
 * The series of failures of a source that lasted into the period [start, end), oldest first (V6, rule 20), as the
 * server tells them: a pass ends a series; a check that did not take place neither ends one nor starts one.
 */
export function failureEpisodes(
  records: Schemas['Verification'][],
  start: number,
  end: number,
): Schemas['ReportFailure'][] {
  const episodes: Schemas['ReportFailure'][] = []
  let open: Schemas['ReportFailure'] | null = null
  for (const record of [...records].reverse()) {
    if (record.status === 'failed') {
      if (open === null) open = { startedAt: record.verifiedAt, records: 0, fixedAt: null }
      open.records += 1
    } else if (record.status === 'passed' && open !== null) {
      episodes.push({ ...open, fixedAt: record.verifiedAt })
      open = null
    }
  }
  if (open !== null) episodes.push(open)
  return episodes.filter(
    (episode) =>
      Date.parse(episode.startedAt) < end &&
      (episode.fixedAt === null || Date.parse(episode.fixedAt) >= start),
  )
}
