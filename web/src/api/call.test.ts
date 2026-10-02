// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { HttpResponse } from 'msw'
import { beforeEach, describe, expect, test } from 'vitest'
import { UnauthenticatedError } from '../auth/session'
import { ids, MOCK_PASSWORD } from '../mocks/fixtures'
import { http } from '../mocks/http'
import { server } from '../mocks/node'
import { ApiError, call, describeError } from './call'
import { client } from './client'

async function signedIn() {
  await client.POST('/api/v1/session', { body: { password: MOCK_PASSWORD } })
}

describe('call', () => {
  beforeEach(signedIn)

  test('returns the body of a successful response', async () => {
    const page = await call(client.GET('/api/v1/agents'))

    expect(page.items.length).toBeGreaterThan(0)
  })

  test('a response without a body is a success too', async () => {
    const result = call(
      client.DELETE('/api/v1/sources/{sourceId}', {
        params: { path: { sourceId: ids.homeSource } },
      }),
    )

    await expect(result).resolves.toBeUndefined()
  })

  test('a problem becomes an ApiError with its machine code and body', async () => {
    const result = call(
      client.GET('/api/v1/agents/{agentId}', {
        params: { path: { agentId: '0192f7a0-0000-7000-8000-00000000ffff' } },
      }),
    )

    await expect(result).rejects.toMatchObject({
      failure: { kind: 'problem', status: 404, code: 'not_found' },
    })
    await expect(result).rejects.toBeInstanceOf(ApiError)
  })

  test('the body of a problem is kept for its extra fields', async () => {
    const error = await call(
      client.POST('/api/v1/sources/{sourceId}/runs', {
        params: { path: { sourceId: ids.etcSource } },
      }),
    ).catch((e: unknown) => e)

    expect(error).toMatchObject({ body: { code: 'run_active', activeRunId: ids.runningRun } })
  })

  test('a broken connection is a network failure', async () => {
    server.use(http.get('/api/v1/agents', () => HttpResponse.error()))

    await expect(call(client.GET('/api/v1/agents'))).rejects.toMatchObject({
      failure: { kind: 'network' },
    })
  })

  test('a 401 is the sign-in signal, not an API error', async () => {
    server.use(http.get('/api/v1/agents', () => new Response(null, { status: 401 })))

    await expect(call(client.GET('/api/v1/agents'))).rejects.toBeInstanceOf(UnauthenticatedError)
  })

  test('an answer that is not a problem document is an HTTP failure', async () => {
    server.use(
      http.get(
        '/api/v1/agents',
        () =>
          new Response('<html>bad gateway</html>', {
            status: 502,
            headers: { 'Content-Type': 'text/html' },
          }),
      ),
    )

    await expect(call(client.GET('/api/v1/agents'))).rejects.toMatchObject({
      failure: { kind: 'http', status: 502 },
    })
  })
})

describe('describeError', () => {
  test('an API failure is described by its code', () => {
    expect(
      describeError(new ApiError({ kind: 'problem', status: 409, code: 'run_active' })),
    ).toEqual({
      key: 'errors.run_active',
    })
  })

  test('anything else is the generic message without its own text', () => {
    const message = describeError(new Error('secret internals'))

    expect(message.key).toBe('errors.generic')
    expect(JSON.stringify(message)).not.toContain('secret')
  })
})
