// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import en from './locales/en.json'
import { errorMessage, failureOf, fieldFailures } from './errors'

const knownCodes = [
  'not_found',
  'validation_failed',
  'unknown_agent',
  'agent_revoked',
  'unknown_plugin',
  'unknown_repository',
  'invalid_config',
  'run_active',
  'token_used',
  'token_expired',
  'origin_rejected',
  'unavailable',
  'not_implemented',
] as const

function problem(status: number, code?: string, extra: Record<string, unknown> = {}) {
  return {
    response: new Response(null, { status }),
    error: { type: 'about:blank', title: 'T', status, detail: null, code, ...extra },
  }
}

describe('an error becomes a message by its machine code', () => {
  test.each(knownCodes)('code %s has its own message key', (code) => {
    const { response, error } = problem(409, code)
    expect(errorMessage(failureOf(response, error))).toEqual({ key: `errors.${code}` })
  })

  test('an unknown code gives the generic message with the code', () => {
    const { response, error } = problem(409, 'quota_exceeded')
    const message = errorMessage(failureOf(response, error))
    expect(message.key).toBe('errors.generic')
    expect(message.params?.detail).toContain('quota_exceeded')
    expect(en.errors.generic).toContain('{{detail}}')
  })

  test('the title and the detail of the server are not in the message', () => {
    const { response, error } = problem(404, 'not_found', { title: 'MARKER-T', detail: 'MARKER-D' })
    expect(JSON.stringify(errorMessage(failureOf(response, error)))).not.toMatch(/MARKER/)
  })

  test.each([
    ['502 with an html body from a proxy', 502, '<html>bad gateway</html>'],
    ['500 with an empty body', 500, undefined],
    ['409 problem json without a code', 409, { type: 'about:blank', title: 'T', status: 409 }],
  ])('%s gives the generic message with the HTTP status', (_name, status, error) => {
    const message = errorMessage(failureOf(new Response(null, { status }), error))
    expect(message.key).toBe('errors.generic')
    expect(message.params?.detail).toContain(String(status))
  })

  test('a connection that broke gives the unavailable-server message', () => {
    expect(errorMessage(failureOf(undefined, new TypeError('Failed to fetch')))).toEqual({
      key: 'errors.network',
    })
  })
})

describe('the fields a 422 names', () => {
  test('a config error shows the server text, any other code its own message', () => {
    const config = {
      code: 'invalid_config',
      errors: [{ field: 'config/paths/0', message: 'must be absolute' }],
    }
    const agent = { code: 'agent_revoked', errors: [{ field: 'agentId', message: 'revoked' }] }

    expect(fieldFailures(config)).toEqual([
      { field: 'config/paths/0', text: { server: 'must be absolute' } },
    ])
    expect(fieldFailures(agent)).toEqual([
      { field: 'agentId', text: { key: 'errors.agent_revoked' } },
    ])
  })

  test('anything that is not a validation problem names no field', () => {
    expect(fieldFailures({ code: 'not_found' })).toEqual([])
    expect(fieldFailures('text')).toEqual([])
    expect(fieldFailures(undefined)).toEqual([])
  })
})
