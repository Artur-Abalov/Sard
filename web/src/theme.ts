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
