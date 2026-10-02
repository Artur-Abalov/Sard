// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createTheme, type MantineColorScheme } from '@mantine/core'

// The single place for visual design: colors, fonts, radii and the
// default color scheme. Light and dark schemes both come from this theme.
export const theme = createTheme({
  primaryColor: 'teal',
  defaultRadius: 'md',
  fontFamily:
    'Inter, system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif',
  headings: { fontWeight: '600' },
})

// "auto" follows the operating system until the user picks a scheme.
export const defaultColorScheme: MantineColorScheme = 'auto'

export interface StatusVisual {
  // A Mantine color name; a status is told by its icon and label too, never by color alone.
  color: string
  icon: string
}

// How each status value looks: runs, steps, tokens and agents share the values that
// mean the same (queued, running, ...). The only place status colors and icons are chosen.
export const statusVisuals: Record<string, StatusVisual> = {
  queued: { color: 'gray', icon: '◔' },
  dispatched: { color: 'cyan', icon: '➜' },
  running: { color: 'blue', icon: '▶' },
  succeeded: { color: 'green', icon: '✔' },
  failed: { color: 'red', icon: '✖' },
  cancelled: { color: 'gray', icon: '⊘' },
  timed_out: { color: 'orange', icon: '⏱' },
  rejected: { color: 'pink', icon: '⛔' },
  lost: { color: 'yellow', icon: '?' },
  active: { color: 'teal', icon: '●' },
  used: { color: 'green', icon: '✔' },
  expired: { color: 'gray', icon: '⌛' },
  revoked: { color: 'red', icon: '⊘' },
  online: { color: 'green', icon: '●' },
  offline: { color: 'gray', icon: '○' },
}

const UNKNOWN_STATUS: StatusVisual = { color: 'gray', icon: '·' }

export function statusVisual(value: string): StatusVisual {
  return Object.hasOwn(statusVisuals, value) ? statusVisuals[value] : UNKNOWN_STATUS
}

// Colors of log levels.
export const logLevelColors: Record<string, string> = {
  debug: 'gray',
  info: 'blue',
  warn: 'yellow',
  error: 'red',
}

// Semantic tones: components say what a message means, the theme says how it looks.
export const tones = {
  error: 'red',
  warning: 'yellow',
  notice: 'orange',
  info: 'blue',
  success: 'green',
  neutral: 'gray',
} as const
