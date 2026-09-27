// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../api/schema'

type Schemas = components['schemas']

/** Response init of every error: the API answers errors as RFC 9457 problems. */
export const PROBLEM = { headers: { 'Content-Type': 'application/problem+json' } }

export function problem(
  status: number,
  title: string,
  code: Schemas['ErrorCode'],
): Schemas['Problem'] {
  return { type: 'about:blank', title, status, detail: null, code }
}

export const noSession = problem(401, 'Unauthorized', 'unauthenticated')
export const notFound = problem(404, 'Not Found', 'not_found')

export function runActive(activeRunId: string): Schemas['RunActiveProblem'] {
  return { ...problem(409, 'Conflict', 'run_active'), activeRunId }
}
