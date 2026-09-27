// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { status } from '../mocks/fixtures'
import { http } from '../mocks/http'
import { server } from '../mocks/node'
import { fetchStatus } from './client'

describe('fetchStatus', () => {
  test('returns the server status', async () => {
    await expect(fetchStatus()).resolves.toEqual(status)
  })

  test('an error response is a failure, not data', async () => {
    server.use(http.get('/api/v1/status', () => new Response(null, { status: 503 })))
    await expect(fetchStatus()).rejects.toThrow('GET /api/v1/status failed')
  })
})

describe('mock server', () => {
  test('a request no handler covers fails instead of reaching the network', async () => {
    await expect(fetch(new URL('/api/v1/no-such-endpoint', location.origin))).rejects.toThrow(
      'Cannot bypass a request when using the "error" strategy',
    )
  })
})
