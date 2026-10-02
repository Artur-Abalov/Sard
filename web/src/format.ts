// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import en from './locales/en.json'
import ru from './locales/ru.json'

// Placeholder shown when there is no value yet.
export const EMPTY = '—'

// Formats an ISO-8601 timestamp from the API for display; null -> EMPTY.
export function formatTimestamp(value: string | null, locale: string): string {
  if (value === null) {
    return EMPTY
  }
  return new Date(value).toLocaleString(locale, {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone: 'UTC',
  })
}

const dictionaries: Record<string, typeof en> = { en, ru }
const BYTE_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB'] as const
const BINARY = 1024

function dictionary(language: string) {
  return dictionaries[language] ?? en
}

// "{{name}}" placeholders of the locale files, for the pure functions that cannot use i18next.
function fill(template: string, values: Record<string, string>): string {
  return template.replace(/\{\{(\w+)\}\}/g, (_, name: string) => values[name] ?? '')
}

// A byte count in binary units with at most one decimal; null -> EMPTY.
export function formatBytes(bytes: number | null, language: string): string {
  if (bytes === null) {
    return EMPTY
  }
  let value = bytes
  let unit = 0
  while (value >= BINARY && unit < BYTE_UNITS.length - 1) {
    value /= BINARY
    unit += 1
  }
  const number = new Intl.NumberFormat(language, {
    maximumFractionDigits: 1,
    useGrouping: false,
  }).format(value)
  return `${number} ${dictionary(language).units[BYTE_UNITS[unit]]}`
}

// Whole percent of the total, capped at 100; null while the total is unknown.
export function formatPercent(processed: number | null, total: number | null): number | null {
  if (processed === null || total === null || total <= 0) {
    return null
  }
  return Math.min(100, Math.floor((processed / total) * 100))
}

const SECONDS_PER_MINUTE = 60
const SECONDS_PER_HOUR = 3600

// Time from start to end, or to now while the step runs; no start -> EMPTY.
export function formatDuration(
  start: string | null,
  end: string | null,
  now: number,
  language: string,
): string {
  if (start === null) {
    return EMPTY
  }
  const until = end === null ? now : Date.parse(end)
  const seconds = Math.max(0, Math.floor((until - Date.parse(start)) / 1000))
  const { units } = dictionary(language)
  if (seconds >= SECONDS_PER_HOUR) {
    const minutes = Math.floor((seconds % SECONDS_PER_HOUR) / SECONDS_PER_MINUTE)
    return `${Math.floor(seconds / SECONDS_PER_HOUR)} ${units.h} ${minutes} ${units.min}`
  }
  if (seconds >= SECONDS_PER_MINUTE) {
    const rest = seconds % SECONDS_PER_MINUTE
    return `${Math.floor(seconds / SECONDS_PER_MINUTE)} ${units.min} ${rest} ${units.s}`
  }
  return `${seconds} ${units.s}`
}

// "N of M files"; "N files" while the total is unknown; nothing without N.
export function formatFiles(
  processed: number | null,
  total: number | null,
  language: string,
): string | null {
  if (processed === null) {
    return null
  }
  const number = new Intl.NumberFormat(language)
  const { filesCount } = dictionary(language)
  if (total === null) {
    return fill(filesCount.processed, { processed: number.format(processed) })
  }
  return fill(filesCount.ofTotal, {
    processed: number.format(processed),
    total: number.format(total),
  })
}
