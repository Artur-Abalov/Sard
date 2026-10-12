// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'
import type { tones } from './theme'

type Warning = components['schemas']['SelfBackupWarningCode']

// What the server already decided (F6), only dressed: the warnings come worst first, and only
// a lasting setting (a local repository, a schedule switched off) is a warning rather than an error.
const SETTINGS: Warning[] = ['local_storage', 'schedule_disabled']

export function bannerTone(warnings: Warning[]): keyof typeof tones | null {
  if (warnings.length === 0) return null
  return warnings.every((warning) => SETTINGS.includes(warning)) ? 'warning' : 'error'
}

// How often the console asks again: the server checks freshness once a minute.
export const SELF_BACKUP_POLL_MS = 60_000
