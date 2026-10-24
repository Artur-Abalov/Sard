// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'
import type { Failure } from './errors'

export type TtlUnit = 'minutes' | 'hours' | 'days'

const SECONDS: Record<TtlUnit, number> = { minutes: 60, hours: 3600, days: 86_400 }

// The ttlSeconds of a new token; undefined when the field is empty, so the request
// carries none and the server's default applies (G6). Its bounds are the server's to check.
export function ttlSeconds(value: number | string, unit: TtlUnit): number | undefined {
  if (value === '') return undefined
  const amount = Number(value)
  return Number.isNaN(amount) ? undefined : Math.round(amount * SECONDS[unit])
}

// Only an active token can be revoked (G8).
export function canRevokeToken(status: components['schemas']['EnrollmentTokenStatus']): boolean {
  return status === 'active'
}

// An id as a short label where no name is known yet.
// Why the server refused to revoke the token: it was used or expired meanwhile. Any other
// failure is no refusal of the token and is shown as an error.
export function revokeRefusal(failure: Failure): 'used' | 'expired' | null {
  if (failure.kind !== 'problem') return null
  if (failure.code === 'token_used') return 'used'
  return failure.code === 'token_expired' ? 'expired' : null
}

export function shortId(id: string): string {
  return id.slice(0, 8)
}
