// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

/** A source no restore verification has reached yet (V4): on, never passed. */
export const neverVerified = {
  verificationEnabled: true,
  verificationStatus: null,
  lastVerifiedAt: null,
  lastVerifiedLevel: null,
} as const

/** The installation's cycle until someone sets it: every night at 04:30 (owner decision 7). */
export const verificationSchedule = {
  cron: '30 4 * * *',
  timezone: 'Europe/Moscow',
  nextRunAt: '2026-09-23T01:30:00Z',
  staleAfterDays: 2,
}
