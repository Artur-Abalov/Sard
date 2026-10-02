// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { UnauthenticatedError } from '../auth/session'
import { errorMessage, failureOf, type Failure, type Message } from '../errors'

// A request the API refused or that never got an answer; the console shows [failure]
// by its machine code (errors.ts), and [body] keeps the problem's extra fields
// (activeRunId of run_active, agentId of token_used, the errors of a 422).
export class ApiError extends Error {
  readonly failure: Failure
  readonly body: unknown

  constructor(failure: Failure, body?: unknown) {
    super(failure.kind === 'problem' ? failure.code : failure.kind)
    this.failure = failure
    this.body = body
  }
}

interface Result<T> {
  data?: T
  error?: unknown
  response: Response
}

// Awaits an openapi-fetch request and returns its body (undefined for an empty one).
// A 401 throws UnauthenticatedError: the query client sends the user to sign in.
export async function call<T>(request: Promise<Result<T>>): Promise<T> {
  let result: Result<T>
  try {
    result = await request
  } catch {
    throw new ApiError({ kind: 'network' })
  }
  if (result.response.ok) {
    return result.data as T
  }
  if (result.response.status === 401) {
    throw new UnauthenticatedError()
  }
  throw new ApiError(failureOf(result.response, result.error), result.error)
}

// The message to show for anything a request threw: an API failure by its code,
// whatever else as the generic one (never the text of the error itself).
export function describeError(error: unknown): Message {
  return error instanceof ApiError
    ? errorMessage(error.failure)
    : { key: 'errors.generic', params: { detail: 'client' } }
}
