// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'

// How often the console asks again: as the other banners of F6, once a minute.
export const REPOSITORIES_POLL_MS = 60_000

/**
 * The number the banner shows, or null for no banner. The server counts the repositories without a recovery key
 * (V3, КВ3); the console only compares its number with zero. No answer, no banner.
 */
export function bannerRepositoryCount(
  list: Pick<components['schemas']['RepositoryList'], 'withoutRecoveryKey'> | undefined,
): number | null {
  return list !== undefined && list.withoutRecoveryKey > 0 ? list.withoutRecoveryKey : null
}
