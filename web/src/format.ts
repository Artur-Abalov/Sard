// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

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
