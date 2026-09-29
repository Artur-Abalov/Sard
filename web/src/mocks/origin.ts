// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { http, type HttpResponseResolver } from 'msw'
import { PROBLEM, problem } from './problems'

/**
 * CSRF parity with the server (К1, К4): a mutating request under /api/v1 with a
 * foreign Origin is 403 origin_rejected. Placed first in handlers.ts so it runs
 * before the per-resource handlers; returning nothing (a same-origin request) lets
 * MSW fall through to them. Registered per method — not `http.all` — so a GET to an
 * unhandled path still falls through to nothing matching (onUnhandledRequest).
 */
const resolver: HttpResponseResolver = ({ request }) => {
  const origin = request.headers.get('Origin')
  if (origin === null || origin === location.origin) return
  return new Response(JSON.stringify(problem(403, 'Forbidden', 'origin_rejected')), {
    status: 403,
    headers: PROBLEM.headers,
  })
}

export const originGuardHandlers = [
  http.post('/api/v1/*', resolver),
  http.put('/api/v1/*', resolver),
  http.patch('/api/v1/*', resolver),
  http.delete('/api/v1/*', resolver),
]
