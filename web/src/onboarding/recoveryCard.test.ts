// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import en from '../locales/en.json'
import ru from '../locales/ru.json'
import { recoveryCardText, type RecoveryCard } from './recoveryCard'

const card: RecoveryCard = {
  repositoryName: 'self',
  backend: 's3',
  location: 's3:acme-sard/prod',
  repositoryId: '0f3a',
  caFingerprint: '8544e235',
  sardVersion: '0.1.0',
  generatedAt: '2026-10-12T12:00:00Z',
  recoveryProcedure: 'https://example.test/06',
}

// docs/specs/web/onboarding-self-backup.feature, rule "Карточка восстановления печатается и скачивается без секретов".
describe('recoveryCardText (Рк15, OQ-202, OQ-203)', () => {
  test.each(['ru', 'en'] as const)('the %s text holds all eight values of the card', (lang) => {
    const text = recoveryCardText(card, lang)
    for (const value of Object.values(card)) expect(text).toContain(value as string)
  })

  test.each([
    ['ru', ru],
    ['en', en],
  ] as const)(
    'the labels of the %s text come from the dictionary of its language',
    (lang, dictionary) => {
      const text = recoveryCardText(card, lang)
      const labels = dictionary.recoveryCard
      for (const key of [
        'repositoryName',
        'backend',
        'location',
        'repositoryId',
        'caFingerprint',
        'sardVersion',
        'generatedAt',
        'recoveryProcedure',
      ] as const) {
        expect(text).toContain(`${labels[key]}: `)
      }
      expect(text.startsWith(labels.title)).toBe(true)
    },
  )

  test('a card without an address says that it is not recorded', () => {
    const text = recoveryCardText({ ...card, location: null }, 'ru')
    expect(text).toContain(`${ru.recoveryCard.location}: ${ru.recoveryCard.locationUnknown}`)
  })

  test('a repository whose id is not read yet is shown as a dash', () => {
    const text = recoveryCardText({ ...card, repositoryId: null }, 'en')
    expect(text).toContain(`${en.recoveryCard.repositoryId}: —`)
  })

  test.each([
    ['ru', ru],
    ['en', en],
  ] as const)('the %s text says what restoring needs', (lang, dictionary) => {
    expect(recoveryCardText(card, lang)).toContain(dictionary.recoveryCard.requires)
  })

  test('has one line per field, in the order of the card, and nothing else about secrets', () => {
    const lines = recoveryCardText(card, 'en').split('\n').filter(Boolean)
    expect(lines).toHaveLength(1 + 1 + 1 + 8)
    expect(recoveryCardText(card, 'en').toLowerCase()).not.toMatch(/secret key|private key/)
  })
})
