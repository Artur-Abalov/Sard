// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../api/schema'
import type { MockState } from './state'

type Schemas = components['schemas']
type Outcome = Schemas['VerificationOutcome'] | null

const DAY_MS = 86_400_000
const ATTENTION = 10

const NOT_HELD: Outcome[] = ['cannot_verify', 'infrastructure_failure', 'no_verifier']

// failed, not held, never verified, the rest: the order the server gives (ОВ10); the oldest pass first within a kind.
function rank(status: Outcome): number {
  if (status === 'failed') return 0
  if (NOT_HELD.includes(status)) return 1
  return status === null ? 3 : 4
}

/** A source with its status as the server tells it: while the installation has no verifier, nothing is held. */
interface Standing {
  source: Schemas['Source']
  status: Outcome
}

function compare(a: Standing, b: Standing): number {
  const passed = ({ source }: Standing) =>
    source.lastVerifiedAt === null ? Number.NEGATIVE_INFINITY : Date.parse(source.lastVerifiedAt)
  return (
    rank(a.status) - rank(b.status) ||
    passed(a) - passed(b) ||
    a.source.name.localeCompare(b.source.name)
  )
}

/** The verification block of the overview by the rules of docs/specs/server/verification-console.feature. */
export function verificationOverview(
  data: Pick<MockState, 'sources' | 'agents'>,
  now: number,
): Schemas['VerificationOverview'] {
  const anyVerifier = data.agents.some((a) => a.role === 'verifier' && a.revokedAt === null)
  const standings: Standing[] = data.sources
    .filter((s) => s.verificationEnabled)
    .map((source) => ({ source, status: anyVerifier ? source.verificationStatus : 'no_verifier' }))
  const count = (matches: (standing: Standing) => boolean) => standings.filter(matches).length
  return {
    enabled: standings.length,
    verifiedLastDay: count(
      ({ source }) =>
        source.lastVerifiedAt !== null && now - Date.parse(source.lastVerifiedAt) <= DAY_MS,
    ),
    failed: count(({ status }) => status === 'failed'),
    notHeld: count(({ status }) => NOT_HELD.includes(status)),
    stale: 0,
    neverVerified: count(({ status }) => status === null),
    disabled: data.sources.length - standings.length,
    noVerifier: standings.length > 0 && !anyVerifier,
    attention: [...standings]
      .sort(compare)
      .slice(0, ATTENTION)
      .map(({ source, status }) => ({
        sourceId: source.id,
        sourceName: source.name,
        status,
        lastVerifiedAt: source.lastVerifiedAt,
        lastVerifiedLevel: source.lastVerifiedLevel,
        staleSince: null,
      })),
  }
}
