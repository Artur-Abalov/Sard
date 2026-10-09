// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import en from './locales/en.json'
import ru from './locales/ru.json'
import { canConfirmRevoke, revokeQuery, SELF_AGENT_CONFIRMATION } from './selfAgent'

describe('revoking an agent', () => {
  test('a built-in agent is revoked only after sard-self is typed, exactly', () => {
    expect(SELF_AGENT_CONFIRMATION).toBe('sard-self')
    expect(canConfirmRevoke(true, 'sard-self')).toBe(true)
    for (const typed of ['', 'sard-sel', 'SARD-SELF', ' sard-self', 'sard-self ', 'yes']) {
      expect(canConfirmRevoke(true, typed), `'${typed}'`).toBe(false)
    }
  })

  test('an ordinary agent needs no typing', () => {
    expect(canConfirmRevoke(false, '')).toBe(true)
    expect(canConfirmRevoke(false, 'anything')).toBe(true)
  })

  test('the request carries confirm for a built-in agent only', () => {
    expect(revokeQuery(true, 'sard-self')).toEqual({ confirm: 'sard-self' })
    expect(revokeQuery(false, 'sard-self')).toEqual({})
    expect(revokeQuery(false, '')).toEqual({})
  })
})

describe('texts of the built-in agent', () => {
  test.each([
    ['ru', ru],
    ['en', en],
  ])('%s has the label, the confirmation field and the refusal', (_name, dictionary) => {
    expect(dictionary.agents.builtin).toBeTruthy()
    expect(dictionary.agent.revokeConfirmLabel).toContain('sard-self')
    expect(dictionary.errors.self_agent_confirmation_required).toBeTruthy()
  })
})
