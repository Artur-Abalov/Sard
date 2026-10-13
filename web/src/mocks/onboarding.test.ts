// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import createClient from 'openapi-fetch'
import { describe, expect, test } from 'vitest'
import type { paths } from '../api/schema'
import { FINGERPRINT } from './api/tokens'
import { resetMockState } from './state'

// The mock onboarding as the wizard sees it: the typed client against the MSW handlers
// (docs/specs/web/onboarding-setup.feature, rule "Моки ведут себя как сервер").
const api = createClient<paths>({ baseUrl: location.origin })
const CODE = 'ABCD-EFGH-JKMN-PQRS-TVWX-YZ01-2345'

function fresh() {
  resetMockState({ signedIn: false, freshInstall: true })
}

const enter = (code: string) => api.POST('/api/v1/onboarding/setup-session', { body: { code } })
const adminStep = (password: string) => api.POST('/api/v1/onboarding/admin', { body: { password } })

describe('the default mock', () => {
  test('reports the admin step done', async () => {
    const { data } = await api.GET('/api/v1/onboarding')
    expect(data).toMatchObject({
      steps: [
        { id: 'ca', state: 'done' },
        { id: 'admin', state: 'done' },
        { id: 'self_backup', state: 'upcoming' },
        { id: 'keys_confirmed', state: 'upcoming' },
      ],
      setupCode: 'not_issued',
      access: 'none',
      ca: null,
      caReplaceable: null,
    })
  })
})

describe('the fresh-install mock', () => {
  test('accepts the mock code and issues a setup session cookie', async () => {
    fresh()
    const { response } = await enter(CODE)
    expect(response.status).toBe(204)
    const cookie = response.headers.get('Set-Cookie') ?? ''
    expect(cookie).toContain('sard_setup')
    expect(cookie).toContain('HttpOnly')
    expect(cookie).toContain('SameSite=Strict')
    expect(cookie).toContain('Path=/api/v1/onboarding')
  })

  test('accepts the code as typed: case, spaces and hyphens do not matter', async () => {
    fresh()
    const { response } = await enter(' abcd efgh jkmn pqrs tvwx yz01 2345 ')
    expect(response.status).toBe(204)
  })

  test('a wrong code is 401 and the sixth attempt after five failures is 429', async () => {
    fresh()
    const wrong = await enter('nope')
    expect(wrong.response.status).toBe(401)
    for (let i = 0; i < 4; i++) await enter('nope')
    const locked = await enter(CODE)
    expect(locked.response.status).toBe(429)
    expect(locked.error?.code).toBe('too_many_attempts')
    expect(locked.response.headers.get('Retry-After')).toBe('900')
  })

  test('describes the CA of the test vector once the code is entered', async () => {
    fresh()
    await enter(CODE)
    const { data } = await api.GET('/api/v1/onboarding')
    expect(data).toMatchObject({
      access: 'setup',
      setupCode: 'active',
      ca: { fingerprint: FINGERPRINT, origin: 'generated' },
      caReplaceable: true,
    })
  })

  test('shows no CA without a setup session', async () => {
    fresh()
    const { data } = await api.GET('/api/v1/onboarding')
    expect(data).toMatchObject({
      access: 'none',
      ca: null,
      caReplaceable: null,
      steps: [
        { id: 'ca', state: 'pending' },
        { id: 'admin', state: 'pending' },
        { id: 'self_backup', state: 'upcoming' },
        { id: 'keys_confirmed', state: 'upcoming' },
      ],
    })
  })

  test('goes through the wizard and then accepts the new password', async () => {
    fresh()
    await enter(CODE)
    expect((await api.POST('/api/v1/onboarding/ca')).response.status).toBe(204)
    const done = await adminStep('mock-admin-pass')
    expect(done.response.status).toBe(204)
    expect(done.response.headers.get('Set-Cookie')).toContain('sard_session')
    const signIn = await api.POST('/api/v1/session', { body: { password: 'mock-admin-pass' } })
    expect(signIn.response.status).toBe(204)
    const again = await enter(CODE)
    expect(again.response.status).toBe(409)
    expect(again.error?.code).toBe('setup_completed')
  })

  test('refuses the admin step before the CA step', async () => {
    fresh()
    await enter(CODE)
    const { response, error } = await adminStep('mock-admin-pass')
    expect(response.status).toBe(409)
    expect(error?.code).toBe('ca_step_pending')
  })

  test('refuses the CA and admin steps without a setup session', async () => {
    fresh()
    expect((await api.POST('/api/v1/onboarding/ca')).response.status).toBe(401)
    expect((await adminStep('mock-admin-pass')).response.status).toBe(401)
  })

  test.each([
    ['short-pw-11', 422],
    ['exactly-12ch', 204],
    ['a'.repeat(1025), 422],
    ['a'.repeat(1024), 204],
  ])('checks the password length like the server: %s', async (password, status) => {
    fresh()
    await enter(CODE)
    await api.POST('/api/v1/onboarding/ca')
    const { response, error } = await adminStep(password)
    expect(response.status).toBe(status)
    if (status === 422) expect(error?.code).toBe('validation_failed')
  })

  test('answers sign-in with 409 setup_required', async () => {
    fresh()
    const { response, error } = await api.POST('/api/v1/session', { body: { password: 'admin' } })
    expect(response.status).toBe(409)
    expect(error?.code).toBe('setup_required')
  })
})

describe('the password change', () => {
  const change = (currentPassword: string, newPassword: string) =>
    api.PUT('/api/v1/session/password', { body: { currentPassword, newPassword } })

  async function signIn() {
    await api.POST('/api/v1/session', { body: { password: 'admin' } })
  }

  test('changes the password and answers with a new session cookie', async () => {
    await signIn()
    const { response } = await change('admin', 'new-password-2026')
    expect(response.status).toBe(204)
    expect(response.headers.get('Set-Cookie')).toContain('sard_session')
    const old = await api.POST('/api/v1/session', { body: { password: 'admin' } })
    expect(old.response.status).toBe(401)
    const fresh = await api.POST('/api/v1/session', { body: { password: 'new-password-2026' } })
    expect(fresh.response.status).toBe(204)
  })

  test('a wrong current password is 422 wrong_password at currentPassword', async () => {
    await signIn()
    const { response, error } = await change('wrong-password', 'new-password-2026')
    expect(response.status).toBe(422)
    expect(error).toMatchObject({
      code: 'wrong_password',
      errors: [{ field: 'currentPassword' }],
    })
  })

  test('a short new password is 422 validation_failed at newPassword', async () => {
    await signIn()
    const { response, error } = await change('admin', 'short')
    expect(response.status).toBe(422)
    expect(error).toMatchObject({
      code: 'validation_failed',
      errors: [{ field: 'newPassword' }],
    })
  })

  test('without a session it is 401', async () => {
    expect((await change('admin', 'new-password-2026')).response.status).toBe(401)
  })

  test('wrong current passwords lock like failed sign-ins', async () => {
    await signIn()
    for (let i = 0; i < 5; i++) await change('nope', 'new-password-2026')
    const locked = await change('admin', 'new-password-2026')
    expect(locked.response.status).toBe(429)
    expect(locked.response.headers.get('Retry-After')).toBe('900')
  })
})
