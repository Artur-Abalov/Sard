// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Compile-time checks only, never run: `npm run typecheck` fails if a mock that
// matches the OpenAPI schema stops compiling or one that breaks it starts to.
import { HttpResponse } from 'msw'
import { enrollmentTokens, scheduleFires, sources } from './fixtures'
import { http } from './http'

const [token] = enrollmentTokens
const [source] = sources
const [fireExample] = Object.values(scheduleFires)[0]

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

  // A token in the list or card cannot carry its string (S2b: shown once, on creation).
  http.get('/api/v1/enrollment-tokens', ({ response }) =>
    // @ts-expect-error: EnrollmentToken has no token property
    response(200).json({ items: [{ ...token, token: 'sard_x.y' }], nextCursor: null }),
  ),

  http.get('/api/v1/enrollment-tokens/{tokenId}', ({ response }) =>
    // @ts-expect-error: EnrollmentToken has no enrollCommand property
    response(200).json({ ...token, enrollCommand: 'sudo -u sard-agent sard-agent enroll' }),
  ),

  http.post('/api/v1/sources', ({ response }) => {
    const { repositoryName: _repositoryName, ...withoutRepository } = source
    // @ts-expect-error: a source always has its repository
    return response(201).json(withoutRepository)
  }),

  http.get('/api/v1/runs/{runId}/steps/{stepId}/logs', ({ response }) =>
    response(200).json({
      // @ts-expect-error: seq is a number
      items: [{ seq: '1', time: '2026-09-27T10:00:00Z', level: 'info', text: 'x' }],
      nextAfterSeq: 1,
      hasMore: false,
    }),
  ),

  http.put('/api/v1/sources/{sourceId}/schedule', ({ response }) =>
    // @ts-expect-error: a schedule always says whether it is enabled
    response(200).json({ id: 'x', sourceId: 'y', cron: '* * * * *', timezone: 'UTC' }),
  ),

  http.get('/api/v1/sources/{sourceId}/schedule/fires', ({ response }) =>
    response(200).json({
      // @ts-expect-error: a fire's outcome is one of the journal's, not a run status
      items: [{ ...fireExample, outcome: 'succeeded' }],
      nextCursor: null,
    }),
  ),

  // @ts-expect-error: a run of a source is created with POST, there is no PUT
  http.put('/api/v1/sources/{sourceId}/runs', () => HttpResponse.json({})),

  http.delete('/api/v1/sources/{sourceId}', ({ response }) =>
    // @ts-expect-error: 409 carries the active run's id
    response(409).json({
      type: 'about:blank',
      title: 'Conflict',
      status: 409,
      detail: null,
      code: 'run_active',
    }),
  ),

  http.post('/api/v1/onboarding/setup-session', ({ response }) =>
    // @ts-expect-error: a 409 is a problem and carries its code
    response(409).json({ type: 'about:blank', title: 'Conflict', status: 409, detail: null }),
  ),
]
