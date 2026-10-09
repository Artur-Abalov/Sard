// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../../api/schema'
import { http } from '../http'
import { pageOf } from '../paging'
import { noSession, notFound, PROBLEM, problem } from '../problems'
import { state } from '../state'
import { validateLabel, validateTtl } from '../validation'

type Schemas = components['schemas']

const DEFAULT_TTL_SECONDS = 86_400
const GRPC_ADDRESS = 'sard.example.com:9090'
// The CA fingerprint of docs/specs/enrollment-token.md's test vector.
export const FINGERPRINT = '8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f'

/** A token string of the documented format: sard_<43 base64url chars>.<64 hex>. */
function tokenString(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(32))
  const secret = btoa(String.fromCharCode(...bytes))
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replace(/=+$/, '')
  return `sard_${secret}.${FINGERPRINT}`
}

function conflict(token: Schemas['EnrollmentToken']): Schemas['TokenConflictProblem'] {
  const code = token.status === 'used' ? 'token_used' : 'token_expired'
  return { ...problem(409, 'Conflict', code), agentId: token.agentId }
}

export const tokenHandlers = [
  http.get('/api/v1/ca', ({ response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    return response(200).json({
      fingerprint: FINGERPRINT,
      origin: 'generated',
      keyPath: '/var/lib/sard/pki/ca/ca.key',
    })
  }),
  http.post('/api/v1/enrollment-tokens', async ({ request, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const { ttlSeconds, label } = await request.json()
    const invalid = validateTtl(ttlSeconds) ?? validateLabel(label)
    if (invalid !== null) return response(422).json(invalid, PROBLEM)
    const now = new Date()
    const expiresAt = new Date(
      now.getTime() + (ttlSeconds ?? DEFAULT_TTL_SECONDS) * 1000,
    ).toISOString()
    const id = crypto.randomUUID()
    const token = tokenString()
    // The mock, like the server, keeps no token string.
    state.tokens.unshift({
      id,
      status: 'active',
      createdAt: now.toISOString(),
      expiresAt,
      usedAt: null,
      revokedAt: null,
      agentId: null,
      label: label === undefined || label === '' ? null : label,
    })
    const enrollCommand = `sudo -u sard-agent sard-agent enroll --server ${GRPC_ADDRESS} --token ${token}`
    return response(201).json({
      id,
      token,
      enrollCommand,
      expiresAt,
      agentEndpointConfigured: false,
    })
  }),

  http.get('/api/v1/enrollment-tokens', ({ query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const status = query.get('status')
    const tokens = state.tokens.filter((t) => status === null || t.status === status)
    return response(200).json(pageOf(tokens, query.get('cursor'), Number(query.get('limit') ?? 50)))
  }),

  http.get('/api/v1/enrollment-tokens/{tokenId}', ({ params, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const token = state.tokens.find((t) => t.id === params.tokenId)
    return token === undefined ? response(404).json(notFound, PROBLEM) : response(200).json(token)
  }),

  http.post('/api/v1/enrollment-tokens/{tokenId}/revoke', ({ params, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const token = state.tokens.find((t) => t.id === params.tokenId)
    if (token === undefined) return response(404).json(notFound, PROBLEM)
    if (token.status === 'used' || token.status === 'expired')
      return response(409).json(conflict(token), PROBLEM)
    if (token.status === 'active')
      Object.assign(token, { status: 'revoked', revokedAt: new Date().toISOString() })
    return response(200).json(token)
  }),
]
