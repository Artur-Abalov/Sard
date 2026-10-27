// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

/** A source without timeouts of its own: the installation's defaults apply (J1b). */
export const noTimeouts = {
  backupTimeoutMinutes: null,
  restoreTimeoutMinutes: null,
} as const
