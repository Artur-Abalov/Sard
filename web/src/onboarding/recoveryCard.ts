// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../api/schema'
import en from '../locales/en.json'
import ru from '../locales/ru.json'

export type RecoveryCard = components['schemas']['RecoveryCard']
export type Language = 'ru' | 'en'

const dictionaries = { ru, en }

/** The fields of the card in the order they are printed. */
export const CARD_FIELDS = [
  'repositoryName',
  'backend',
  'location',
  'repositoryId',
  'caFingerprint',
  'sardVersion',
  'generatedAt',
  'recoveryProcedure',
] as const

/**
 * The card as plain text for the file the owner downloads (Рк15, OQ-202): the title, the note, then a line per
 * field with its label in [language]. The card holds no password and no credentials; this text holds nothing
 * more. A field the server does not know is a dash, an address nobody recorded says so.
 */
export function recoveryCardText(card: RecoveryCard, language: Language): string {
  const words = dictionaries[language].recoveryCard
  const valueOf = (field: (typeof CARD_FIELDS)[number]): string => {
    const value = card[field]
    if (value === null) return field === 'location' ? words.locationUnknown : '—'
    return value
  }
  const lines = CARD_FIELDS.map((field) => `${words[field]}: ${valueOf(field)}`)
  return [words.title, words.intro, ...lines].join('\n') + '\n'
}
