// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Compile-time checks only, never run: `npm run typecheck` fails if a mock that
// matches the OpenAPI schema stops compiling or one that breaks it starts to.
import { HttpResponse } from 'msw'
import { http } from './http'

export const contractChecks = [
  http.get('/api/v1/status', ({ response }) =>
    response(200).json({ version: '1.0.0', lastVerifiedRestoreAt: null }),
  ),

  // @ts-expect-error: /api/v1/nope is not in the schema
  http.get('/api/v1/nope', () => HttpResponse.json({})),

  http.get('/api/v1/status', ({ response }) =>
    // @ts-expect-error: lastVerifiedRestoreAt is required
    response(200).json({ version: '1.0.0' }),
  ),

  http.get('/api/v1/status', ({ response }) =>
    // @ts-expect-error: version is a string
    response(200).json({ version: 1, lastVerifiedRestoreAt: null }),
  ),

  // @ts-expect-error: the schema declares no 404 response for /status
  http.get('/api/v1/status', ({ response }) => response(404).empty()),

  // @ts-expect-error: a plain HttpResponse is checked against the schema too
  http.get('/api/v1/status', () => HttpResponse.json({ version: 1 })),
]
