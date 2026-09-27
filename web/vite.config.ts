// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

// The dev server proxies API calls to a locally running sard-server.
const server = process.env.SARD_SERVER_URL ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': server,
      '/actuator': server,
      '/v3/api-docs': server,
    },
  },
  test: {
    include: ['src/**/*.test.ts'],
    environment: 'node',
  },
})
