// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

/** Retry-After seconds shown as whole minutes, rounded up (Р9ж). */
export function formatLockoutMinutes(retryAfterSeconds: number): number {
  return Math.ceil(retryAfterSeconds / 60)
}
