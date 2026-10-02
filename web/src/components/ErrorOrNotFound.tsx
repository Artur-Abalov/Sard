// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { ReactNode } from 'react'
import { ApiError } from '../api/call'
import { ErrorBlock } from './ErrorBlock'
import { NotFoundCard } from './NotFoundCard'

// A failed card load: "not found" with a way back for a 404, the error with a retry otherwise.
export function ErrorOrNotFound({
  error,
  onRetry,
  back,
}: {
  error: unknown
  onRetry: () => void
  back: ReactNode
}) {
  const missing =
    error instanceof ApiError && error.failure.kind === 'problem' && error.failure.status === 404
  return missing ? (
    <NotFoundCard>{back}</NotFoundCard>
  ) : (
    <ErrorBlock error={error} onRetry={onRetry} />
  )
}
