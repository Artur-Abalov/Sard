// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

// The dev server proxies API calls to a locally running sard-server.
const server = process.env.SARD_SERVER_URL ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [
    // Generates src/routes -> src/routeTree.gen.ts (settings in tsr.config.json);
    // must run before the React plugin.
    tanstackRouter({ target: 'react' }),
    react(),
  ],
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
