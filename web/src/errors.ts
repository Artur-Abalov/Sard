// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'

type ErrorCode = components['schemas']['ErrorCode']

// What went wrong with a request: an API problem with its machine code, an
// answer that is not a problem document, or no answer at all.
export type Failure =
  | { kind: 'problem'; status: number; code: string }
  | { kind: 'http'; status: number }
  | { kind: 'network' }

export interface Message {
  // A key of src/locales; the text is never taken from the server's title or detail.
  key: string
  params?: { detail: string }
}

// Every code of the contract; a new one in openapi.json breaks the type check here.
const KNOWN: Record<ErrorCode, true> = {
  unauthenticated: true,
  too_many_attempts: true,
  not_found: true,
  validation_failed: true,
  unknown_agent: true,
  unknown_plugin: true,
  unknown_repository: true,
  invalid_config: true,
  run_active: true,
  token_used: true,
  token_expired: true,
  not_implemented: true,
  origin_rejected: true,
  agent_revoked: true,
  unavailable: true,
  self_agent_confirmation_required: true,
  system_source: true,
  self_agent_missing: true,
  repository_not_initialized: true,
  local_storage_unconfirmed: true,
  self_backup_not_configured: true,
}

function codeOf(error: unknown): string | null {
  if (typeof error !== 'object' || error === null || !('code' in error)) {
    return null
  }
  return typeof error.code === 'string' ? error.code : null
}

// Classifies the outcome of an openapi-fetch call; `response` is undefined when fetch threw.
export function failureOf(response: Response | undefined, error: unknown): Failure {
  if (response === undefined) {
    return { kind: 'network' }
  }
  const code = codeOf(error)
  return code === null
    ? { kind: 'http', status: response.status }
    : { kind: 'problem', status: response.status, code }
}

export function errorMessage(failure: Failure): Message {
  if (failure.kind === 'network') {
    return { key: 'errors.network' }
  }
  if (failure.kind === 'problem' && failure.code in KNOWN) {
    return { key: `errors.${failure.code}` }
  }
  const detail = failure.kind === 'problem' ? `${failure.status} ${failure.code}` : failure.status
  return { key: 'errors.generic', params: { detail: String(detail) } }
}

export interface FieldFailure {
  // A JSON Pointer without the leading slash, e.g. "config/paths/0".
  field: string
  // The server's own words for a config error, a localized message otherwise.
  text: { server: string } | { key: string }
}

// The fields a 422 names, each with the text to show next to it.
export function fieldFailures(error: unknown): FieldFailure[] {
  const code = codeOf(error)
  if (code === null || typeof error !== 'object' || error === null || !('errors' in error)) {
    return []
  }
  const errors = Array.isArray(error.errors)
    ? (error.errors as components['schemas']['FieldError'][])
    : []
  return errors.map(({ field, message }) => ({
    field,
    text:
      code === 'invalid_config'
        ? { server: message }
        : { key: errorMessage({ kind: 'problem', status: 422, code }).key },
  }))
}
