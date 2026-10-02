// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { useTranslation } from 'react-i18next'
import { formatBytes, formatDuration, formatFiles, formatRelative, formatTimestamp } from './format'

// The formatters of format.ts bound to the interface language.
export function useFormat() {
  const { i18n } = useTranslation()
  const language = i18n.language
  return {
    time: (value: string | null) => formatTimestamp(value, language),
    relative: (value: string | null) => formatRelative(value, Date.now(), language),
    bytes: (value: number | null) => formatBytes(value, language),
    duration: (start: string | null, end: string | null) =>
      formatDuration(start, end, Date.now(), language),
    files: (processed: number | null, total: number | null) =>
      formatFiles(processed, total, language),
  }
}
