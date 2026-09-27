// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Official Mantine setup for Vite: https://mantine.dev/guides/vite/
module.exports = {
  plugins: {
    'postcss-preset-mantine': {},
    'postcss-simple-vars': {
      variables: {
        'mantine-breakpoint-xs': '36em',
        'mantine-breakpoint-sm': '48em',
        'mantine-breakpoint-md': '62em',
        'mantine-breakpoint-lg': '75em',
        'mantine-breakpoint-xl': '88em',
      },
    },
  },
}
