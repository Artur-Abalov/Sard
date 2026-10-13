// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import {
  adminStepResult,
  answerFrom,
  caStepResult,
  codeResult,
  passwordChangeResult,
  type Answer,
} from './outcomes'

const answer = (status: number, code: string | null = null, retryAfter = 0): Answer => ({
  status,
  code,
  retryAfter,
})

describe('answerFrom', () => {
  test('takes the machine code of a problem body and Retry-After seconds', () => {
    expect(answerFrom(429, '900', { code: 'too_many_attempts' })).toEqual({
      status: 429,
      code: 'too_many_attempts',
      retryAfter: 900,
    })
  })

  test('has no code for an empty or foreign body and zero for a missing Retry-After', () => {
    expect(answerFrom(204, null, undefined)).toEqual({ status: 204, code: null, retryAfter: 0 })
    expect(answerFrom(500, null, { code: 5 })).toEqual({ status: 500, code: null, retryAfter: 0 })
  })

  test('a Retry-After that is not a number counts as zero', () => {
    expect(answerFrom(429, 'soon', null).retryAfter).toBe(0)
  })
})

describe('codeResult', () => {
  test('204 moves on', () => {
    expect(codeResult(answer(204))).toEqual({ kind: 'done' })
  })

  test('401 stays with the wrong-code notice', () => {
    expect(codeResult(answer(401, 'unauthenticated'))).toEqual({
      kind: 'stay',
      notice: { key: 'setup.code.invalid' },
    })
  })

  test('429 stays with the lock notice in whole minutes', () => {
    expect(codeResult(answer(429, 'too_many_attempts', 900))).toEqual({
      kind: 'stay',
      notice: { key: 'setup.code.locked', minutes: 15 },
    })
  })

  test('409 goes to sign-in saying the administrator exists', () => {
    expect(codeResult(answer(409, 'setup_completed'))).toEqual({
      kind: 'goto',
      target: 'login',
      notice: { key: 'setup.completed' },
    })
  })

  test('anything else is the server being unavailable', () => {
    expect(codeResult(answer(503, 'unavailable'))).toEqual({
      kind: 'stay',
      notice: { key: 'setup.unavailable' },
    })
  })
})

describe('caStepResult', () => {
  test('204 moves on', () => {
    expect(caStepResult(answer(204))).toEqual({ kind: 'done' })
  })

  test('401 returns to the code with the ended-session notice', () => {
    expect(caStepResult(answer(401))).toEqual({
      kind: 'goto',
      target: 'code',
      notice: { key: 'setup.sessionEnded' },
    })
  })

  test('409 means the administrator exists already', () => {
    expect(caStepResult(answer(409, 'setup_completed'))).toMatchObject({
      kind: 'goto',
      target: 'login',
    })
  })

  test('503 stays on the step', () => {
    expect(caStepResult(answer(503))).toEqual({
      kind: 'stay',
      notice: { key: 'setup.unavailable' },
    })
  })
})

describe('adminStepResult', () => {
  test('204 moves on', () => {
    expect(adminStepResult(answer(204))).toEqual({ kind: 'done' })
  })

  test('401 returns to the code with the ended-session notice', () => {
    expect(adminStepResult(answer(401))).toEqual({
      kind: 'goto',
      target: 'code',
      notice: { key: 'setup.sessionEnded' },
    })
  })

  test('409 ca_step_pending returns to the CA step without a notice', () => {
    expect(adminStepResult(answer(409, 'ca_step_pending'))).toEqual({
      kind: 'goto',
      target: 'ca',
      notice: null,
    })
  })

  test('409 setup_completed goes to sign-in', () => {
    expect(adminStepResult(answer(409, 'setup_completed'))).toMatchObject({
      kind: 'goto',
      target: 'login',
    })
  })

  test('422 stays and shows the requirements', () => {
    expect(adminStepResult(answer(422, 'validation_failed'))).toEqual({
      kind: 'stay',
      notice: { key: 'setup.admin.requirements' },
    })
  })

  test('anything else is the server being unavailable', () => {
    expect(adminStepResult(answer(500))).toEqual({
      kind: 'stay',
      notice: { key: 'setup.unavailable' },
    })
  })
})

describe('passwordChangeResult', () => {
  test('204 is done', () => {
    expect(passwordChangeResult(answer(204))).toEqual({ kind: 'done' })
  })

  test('422 wrong_password stays with its notice', () => {
    expect(passwordChangeResult(answer(422, 'wrong_password'))).toEqual({
      kind: 'stay',
      notice: { key: 'settings.wrongPassword' },
    })
  })

  test('another 422 shows the requirements', () => {
    expect(passwordChangeResult(answer(422, 'validation_failed'))).toEqual({
      kind: 'stay',
      notice: { key: 'settings.requirements' },
    })
  })

  test('429 shows the lock in whole minutes', () => {
    expect(passwordChangeResult(answer(429, 'too_many_attempts', 61))).toEqual({
      kind: 'stay',
      notice: { key: 'settings.locked', minutes: 2 },
    })
  })

  test('401 goes to sign-in', () => {
    expect(passwordChangeResult(answer(401))).toMatchObject({ kind: 'goto', target: 'login' })
  })

  test('501 says the administrator is managed elsewhere', () => {
    expect(passwordChangeResult(answer(501))).toEqual({
      kind: 'stay',
      notice: { key: 'settings.notSupported' },
    })
  })

  test('anything else is the server being unavailable', () => {
    expect(passwordChangeResult(answer(503))).toEqual({
      kind: 'stay',
      notice: { key: 'settings.unavailable' },
    })
  })
})
